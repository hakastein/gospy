package app_test

import (
	"context"
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

func TestRunHoldsAProcessWhosePhpspyExitedWhileItLives(t *testing.T) {
	t.Parallel()

	// phpspy returns 0 only when the traced process died; a process still in the next scan
	// did not, so the exit is a failed attach and not a reason to run objdump every scan.
	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "echo start >> \"$MARKER_DIR/starts.$pid\"\nexit 0",
	})
	table := &processTable{}
	table.set(worker(201))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: &captureTransport{}, Processes: table})

	starts := filepath.Join(markers, "starts.201")
	require.Eventually(t, func() bool {
		return markerLines(t, starts) == 1
	}, shutdownSafetyNet, 20*time.Millisecond, "the process was never attached")
	time.Sleep(settleTime)

	cancel()
	require.NoError(t, awaitRun(t, done))

	require.Equal(t, 1, markerLines(t, starts), "an exit 0 on a live process is held like a failure")
}

func TestRunFreesTheSlotOfAProcessThatEnded(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: "echo start >> \"$MARKER_DIR/starts.$pid\"\nexit 0",
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

	require.Eventually(t, func() bool {
		return markerLines(t, filepath.Join(markers, "starts.201")) == 1
	}, shutdownSafetyNet, 20*time.Millisecond, "the process was never attached")

	// The process is gone from the next scan, as phpspy's exit said: its slot goes to the newcomer.
	table.set(worker(202))
	require.Eventually(t, func() bool {
		return len(transport.captured()) > 0
	}, shutdownSafetyNet, 20*time.Millisecond, "the slot of the ended process was never handed on")

	cancel()
	require.NoError(t, awaitRun(t, done))
	require.Equal(t, peekGlobalStacks("checkout"), countStacks(t, transport.captured()))
}

func TestRunDetachesAProcessThatLeftTheTarget(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		201: replayAndHold(t, "peek-global.txt"),
	})
	table := &processTable{}
	table.set(worker(201))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: &captureTransport{}, Processes: table})

	pid := pidOf(t, filepath.Join(markers, "pid.201"))

	// A process the scan no longer lists for the target, here because it exited, is detached
	// rather than traced until its phpspy notices.
	table.set()
	requireGone(t, pid)

	cancel()
	require.NoError(t, awaitRun(t, done))
}

func TestRunLetsADisabledTargetClaimItsProcesses(t *testing.T) {
	t.Parallel()

	executable, markers := scriptedPhpspy(t, map[int]string{
		300: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
		301: "touch \"$MARKER_DIR/started.$pid\"\nexec sleep 60",
	})
	table := &processTable{}
	table.set(listener(300), script(301))

	// The queue target is switched off and comes first: its listeners are not profiled by
	// the broader cli target below it, while a plain script still falls through to cli.
	cfg := loadConfig(t, `
pyroscope: { url: `+pyroscopeURL+` }
app: checkout
phpspy: `+executable+`
stats-interval: 0
targets:
  - name: queue
    match: { comm: php, cmdline: 'queue:listen' }
    max-processes: 0
  - name: cli
    match: { comm: php }
    max-processes: 2
    scan-interval: 100ms
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: &captureTransport{}, Processes: table})

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(markers, "started.301"))
		return err == nil
	}, shutdownSafetyNet, 20*time.Millisecond, "the script never reached the cli target")
	time.Sleep(settleTime)

	cancel()
	require.NoError(t, awaitRun(t, done))

	require.NoFileExists(t, filepath.Join(markers, "started.300"), "a disabled target's process must not fall through to a later target")
}

func TestRunKeepsScanningAfterAFailedScan(t *testing.T) {
	t.Parallel()

	executable, _ := scriptedPhpspy(t, map[int]string{201: replayAndHold(t, "peek-global.txt")})
	table := &failingTable{}
	table.failures.Store(3)
	table.set(worker(201))
	transport := &captureTransport{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: checkoutConfig(t, executable, ""), Transport: transport, Processes: table})

	require.Eventually(t, func() bool {
		return len(transport.captured()) > 0
	}, shutdownSafetyNet, 20*time.Millisecond, "a scan that failed must be retried on the next interval")

	cancel()
	require.NoError(t, awaitRun(t, done))
	require.Equal(t, peekGlobalStacks("checkout"), countStacks(t, transport.captured()))
}

func TestRunRotatesASlotBetweenWaitingProcesses(t *testing.T) {
	t.Parallel()

	// 202 replays a fixture without meta lines, so its samples land in a series of their own.
	executable, _ := scriptedPhpspy(t, map[int]string{
		201: replayAndHold(t, "peek-global.txt"),
		202: replayAndHold(t, "request-info.txt"),
	})
	table := &processTable{}
	table.set(worker(201), worker(202))
	transport := &captureTransport{}

	cfg := checkoutConfig(t, executable, "")
	cfg.Targets[0].MaxProcesses = 1
	cfg.Targets[0].Rotate = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startRun(ctx, app.Config{Config: cfg, Transport: transport, Processes: table})

	require.Eventually(t, func() bool {
		return len(profileNames(transport.captured())) == 3
	}, shutdownSafetyNet, 20*time.Millisecond, "the single slot never rotated to the waiting process")

	cancel()
	require.NoError(t, awaitRun(t, done))

	profiles := countStacks(t, transport.captured())
	require.Contains(t, profiles, "checkout{env=production,source=fpm,uri=/orders/42}")
	require.Equal(t, map[string]int{
		`main /srv/app/public/index.php;App\Controller\OrderController::show;App\Repository\OrderRepository::find;mysqli::query`: 2,
		`main /srv/app/bin/console;App\Queue\Worker::consume`:                                                                    1,
	}, profiles["checkout{env=production,source=fpm}"], "everything the rotated-in process printed must be delivered")
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

	requireGone(t, pidOf(t, filepath.Join(markers, "pid.201")))

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
				writeExecutable(t, path, "#!/nonexistent/interpreter\n")
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
		requireGone(t, pid)
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

			requireGone(t, pid)
		})
	}
}
