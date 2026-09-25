package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/procscan"
)

func TestRunProfilesEveryTargetWithItsOwnTagsAndRate(t *testing.T) {
	t.Parallel()

	executable, _ := scriptedPhpspy(t, map[int]string{
		201: replayAndHold(t, "peek-global.txt"),
		300: replayAndHold(t, "request-info.txt"),
	})
	table := &processTable{}
	table.set(worker(201), listener(300), procscan.Process{PID: 500, ParentPID: 1, StartTime: 1500, Comm: "nginx", Cmdline: "nginx: worker process"})
	transport := &captureTransport{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startedAt := time.Now()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: transport, Processes: table})

	require.Eventually(t, func() bool {
		return len(profileNames(transport.captured())) == 3
	}, shutdownSafetyNet, 20*time.Millisecond, "the samples of both targets never reached pyroscope")

	cancel()
	require.NoError(t, awaitRun(t, done))
	finishedAt := time.Now()

	requests := transport.captured()
	for _, request := range requests {
		require.Equal(t, "POST", request.method)
		require.Equal(t, "/ingest", request.path)
		require.Equal(t, "folded", request.query.Get("format"))

		from, err := strconv.ParseInt(request.query.Get("from"), 10, 64)
		require.NoError(t, err)
		until, err := strconv.ParseInt(request.query.Get("until"), 10, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, from, startedAt.Unix())
		require.GreaterOrEqual(t, until, from)
		require.LessOrEqual(t, until, finishedAt.Unix())
	}

	require.Equal(t, map[string]string{
		"checkout{env=production,source=fpm,uri=/orders/42}":                         "25",
		"checkout{env=production,source=fpm,uri=/cart}":                              "25",
		"checkout{env=production,source=queue,entrypoint=/srv/app/public/index.php}": "10",
	}, sampleRates(requests), "each request carries the rate of the target that produced it")

	require.Equal(t, merge(peekGlobalStacks("checkout"), requestInfoStacks("checkout")), countStacks(t, requests), "every process was attached exactly once")
}

func TestRunStartsNothingForADisabledTarget(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
		300: replayAndHold(t, "request-info.txt"),
	})
	table := &processTable{}
	table.set(worker(201), listener(300))
	transport := &captureTransport{}

	cfg := checkoutConfig(t, executable, "")
	cfg.Targets[0].MaxProcesses = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: transport, Processes: table})

	require.Eventually(t, func() bool {
		return len(transport.captured()) > 0
	}, shutdownSafetyNet, 20*time.Millisecond, "the enabled target never delivered")
	time.Sleep(settleTime)

	cancel()
	require.NoError(t, awaitRun(t, done))

	require.NoFileExists(t, filepath.Join(markers, "started.201"), "a target with max-processes 0 must attach nothing")
	require.Equal(t, requestInfoStacks("checkout"), countStacks(t, transport.captured()))
}

func TestRunFreesTheSlotOfAFailedAttachAndHoldsTheProcess(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "echo start >> \"$MARKER_DIR/starts.$pid\"\necho 'phpspy: cannot attach' >&2\nexit 1",
		202: replayAndHold(t, "peek-global.txt"),
	})
	table := &processTable{}
	table.set(worker(201))
	transport := &captureTransport{}

	cfg := checkoutConfig(t, executable, "")
	cfg.Targets[0].MaxProcesses = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: transport, Processes: table})

	starts := filepath.Join(markers, "starts.201")
	require.Eventually(t, func() bool {
		return markerLines(t, starts) == 1
	}, shutdownSafetyNet, 20*time.Millisecond, "the failing process was never attached")

	// A second worker appears while the first sits in its hold: the only slot must go to it.
	table.set(worker(201), worker(202))
	require.Eventually(t, func() bool {
		return len(transport.captured()) > 0
	}, shutdownSafetyNet, 20*time.Millisecond, "the slot the failed attach held was never handed on")
	time.Sleep(settleTime)

	cancel()
	require.NoError(t, awaitRun(t, done))

	require.Equal(t, 1, markerLines(t, starts), "a failed attach is held, not retried on every scan")
	require.Equal(t, peekGlobalStacks("checkout"), countStacks(t, transport.captured()))
}

func TestRunReattachesAProcessWhosePhpspyExitedCleanly(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "echo start >> \"$MARKER_DIR/starts.$pid\"\nexit 0",
	})
	table := &processTable{}
	table.set(worker(201))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: &captureTransport{}, Processes: table})

	require.Eventually(t, func() bool {
		return markerLines(t, filepath.Join(markers, "starts.201")) >= 3
	}, shutdownSafetyNet, 20*time.Millisecond, "a clean exit must free the slot without a hold")

	cancel()
	require.NoError(t, awaitRun(t, done))
}

func TestRunRotatesASlotBetweenWaitingProcesses(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: replayAndHold(t, "peek-global.txt"),
		202: replayAndHold(t, "peek-global.txt"),
	})
	table := &processTable{}
	table.set(worker(201), worker(202))

	cfg := checkoutConfig(t, executable, "")
	cfg.Targets[0].MaxProcesses = 1
	cfg.Targets[0].Rotate = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: &captureTransport{}, Processes: table})

	for _, pid := range []int{201, 202} {
		pidOf(t, filepath.Join(markers, "pid."+strconv.Itoa(pid)))
	}

	cancel()
	require.NoError(t, awaitRun(t, done))
}

func TestRunKillsAnAttachThatIsSilentlyDenied(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "echo $$ > \"$MARKER_DIR/pid.$pid\"\nwhile true; do echo 'copy_proc_mem: Failed to read memory: Operation not permitted' >&2; sleep 0.02; done",
	})
	table := &processTable{}
	table.set(worker(201))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{
		Config:              checkoutConfig(t, executable, ""),
		Transport:           &captureTransport{},
		Processes:           table,
		SilentAttachTimeout: 200 * time.Millisecond,
	})

	pid := pidOf(t, filepath.Join(markers, "pid.201"))
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, shutdownSafetyNet, 20*time.Millisecond, "the denied attach was left running")

	cancel()
	require.NoError(t, awaitRun(t, done))
}

func TestRunNeverAttachesToItsOwnDescendants(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
		202: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
		203: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
	})

	// 202 looks like a worker but is gospy's child, 203 its grandchild through a helper.
	child := worker(202)
	child.ParentPID = os.Getpid()
	grandchild := worker(203)
	grandchild.ParentPID = 202
	table := &processTable{}
	table.set(worker(201), child, grandchild)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: &captureTransport{}, Processes: table})

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(markers, "started.201"))
		return err == nil
	}, shutdownSafetyNet, 20*time.Millisecond, "the ordinary worker was never attached")
	time.Sleep(settleTime)

	cancel()
	require.NoError(t, awaitRun(t, done))

	require.NoFileExists(t, filepath.Join(markers, "started.202"), "gospy's own child must never be a candidate")
	require.NoFileExists(t, filepath.Join(markers, "started.203"), "gospy's grandchild must never be a candidate")
}

func TestRunFailsWhenPhpspyCannotBeStarted(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		executable func(t *testing.T) string
		wantErr    string
	}{
		{
			name: "missing binary is caught before anything starts",
			executable: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "phpspy")
			},
			wantErr: "phpspy cannot be started",
		},
		{
			name: "binary that cannot be executed is caught on the first attach",
			executable: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "phpspy")
				require.NoError(t, os.WriteFile(path, []byte("#!/nonexistent/interpreter\n"), 0o755))
				return path
			},
			wantErr: "cannot start",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			table := &processTable{}
			table.set(worker(201))

			err := awaitRun(t, startRun(context.Background(), app.Config{
				Config:    checkoutConfig(t, tc.executable(t), ""),
				Transport: &captureTransport{},
				Processes: table,
			}))
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRunKeepsRunningWithoutAnyProcess(t *testing.T) {
	t.Parallel()

	executable, _ := scriptedPhpspy(t, nil)
	table := &processTable{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: &captureTransport{}, Processes: table})

	select {
	case err := <-done:
		t.Fatalf("Run returned with nothing to profile: %v", err)
	case <-time.After(settleTime):
	}

	cancel()
	require.NoError(t, awaitRun(t, done))
}

func TestRunDeliversEverySampleOnCancellation(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: replayAndHold(t, "peek-global.txt"),
		300: replayAndHold(t, "request-info.txt"),
	})
	table := &processTable{}
	table.set(worker(201), listener(300))
	transport := &captureTransport{}

	// A minute-long window means nothing ships before the drain does.
	cfg := checkoutConfig(t, executable, "")
	cfg.BatchInterval = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: transport, Processes: table})

	pids := []int{pidOf(t, filepath.Join(markers, "pid.201")), pidOf(t, filepath.Join(markers, "pid.300"))}
	time.Sleep(settleTime)
	require.Empty(t, transport.captured(), "nothing may ship before the window closes")

	cancel()
	require.NoError(t, awaitRun(t, done), "a shutdown is not a failed run")

	require.Equal(t, merge(peekGlobalStacks("checkout"), requestInfoStacks("checkout")), countStacks(t, transport.captured()), "every sample accepted before the shutdown must be delivered")
	for _, pid := range pids {
		require.Eventually(t, func() bool {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		}, shutdownSafetyNet, 10*time.Millisecond, "phpspy %d outlived the shutdown", pid)
	}
}

func TestRunEndsTheDrainAtItsDeadline(t *testing.T) {
	t.Parallel()

	url, started := hangingPyroscope(t)
	executable, _ := scriptedPhpspy(t, map[int]string{201: replayAndHold(t, "peek-global.txt")})
	table := &processTable{}
	table.set(worker(201))

	cfg := checkoutConfig(t, executable, "drain-timeout: 100ms")
	cfg.Pyroscope.URL = url
	cfg.Pyroscope.Timeout = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startRun(ctx, app.Config{Config: cfg, Processes: table})
	awaitSignal(t, started, "no batch reached pyroscope")

	cancel()
	require.NoError(t, awaitRun(t, done), "a drain cut short by its deadline is not a failed run")
}

// Not parallel: a signal sent to the test process reaches every Run listening at the time.
func TestRunStopsOnASignalAndAbortsTheDrainOnASecond(t *testing.T) {
	testCases := []struct {
		name     string
		shutdown func(t *testing.T, cancel context.CancelFunc)
		wantErr  error
	}{
		{
			name: "one SIGTERM drains",
			shutdown: func(t *testing.T, _ context.CancelFunc) {
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
			},
		},
		{
			name: "cancelled context, then SIGTERM",
			shutdown: func(t *testing.T, cancel context.CancelFunc) {
				cancel()
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
			},
			wantErr: app.ErrDrainAborted,
		},
		{
			name: "SIGTERM, then SIGINT",
			shutdown: func(t *testing.T, _ context.CancelFunc) {
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
			},
			wantErr: app.ErrDrainAborted,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, started := hangingPyroscope(t)
			executable, markers := scriptedPhpspy(t, map[int]string{201: replayAndHold(t, "peek-global.txt")})
			table := &processTable{}
			table.set(worker(201))

			cfg := checkoutConfig(t, executable, "drain-timeout: 300ms")
			cfg.Pyroscope.URL = url
			cfg.Pyroscope.Timeout = time.Minute
			if tc.wantErr != nil {
				cfg.DrainTimeout = time.Minute
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := startRun(ctx, app.Config{Config: cfg, Processes: table})
			pid := pidOf(t, filepath.Join(markers, "pid.201"))
			awaitSignal(t, started, "no batch reached pyroscope")

			tc.shutdown(t, cancel)
			err := awaitRun(t, done)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}

			require.Eventually(t, func() bool {
				return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
			}, shutdownSafetyNet, 10*time.Millisecond, "phpspy outlived the shutdown")
		})
	}
}
