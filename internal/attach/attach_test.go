package attach_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/attach"
	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
)

const (
	testTimeout = 20 * time.Second
	sampleRate  = 25
)

const traceBlock = "# glopeek server.REQUEST_URI = /orders/42\n" +
	"0 App\\\\Kernel::handle /srv/app/src/Kernel.php:12\n" +
	"1 main /srv/app/public/index.php:1\n\n"

func script(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "phpspy")
	writeExecutable(t, path, "#!/bin/sh\n"+body)

	return path
}

// writeExecutable holds off every fork in the test binary while the file is open for writing:
// a child forked by a parallel test in that window inherits the descriptor until it execs,
// and an exec of the script meanwhile fails with ETXTBSY.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()

	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func config(executable string) attach.Config {
	return attach.Config{
		Executable: executable,
		PID:        4242,
		Rate:       sampleRate,
		Parser: phpspy.ParserConfig{
			DynamicTags:        map[string][]tag.DynamicTag{"glopeek server.REQUEST_URI": {{TagKey: "uri"}}},
			KeepEntrypointName: true,
		},
		Logger: zerolog.Nop(),
	}
}

func awaitResult(t *testing.T, a *attach.Attach) attach.Result {
	t.Helper()

	results := make(chan attach.Result, 1)
	go func() { results <- a.Wait() }()

	select {
	case result := <-results:
		return result
	case <-time.After(testTimeout):
		t.Fatal("the attach did not end in time")
		return attach.Result{}
	}
}

// stillRunning asserts that the attach has not ended within the grace.
func stillRunning(t *testing.T, a *attach.Attach, grace time.Duration) {
	t.Helper()

	results := make(chan attach.Result, 1)
	go func() { results <- a.Wait() }()

	select {
	case result := <-results:
		t.Fatalf("the attach ended: %+v", result)
	case <-time.After(grace):
	}
}

func drain(samples <-chan *collector.Sample) []*collector.Sample {
	var collected []*collector.Sample
	for {
		select {
		case sample := <-samples:
			collected = append(collected, sample)
		default:
			return collected
		}
	}
}

// pidOf reads the PID a script wrote with `echo $$ > file` once the line is complete.
func pidOf(t *testing.T, file string) int {
	t.Helper()

	var pid int
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(file)
		if err != nil || !strings.HasSuffix(string(content), "\n") {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(content)))
		return err == nil
	}, testTimeout, 10*time.Millisecond, "the script never wrote its pid")

	return pid
}

func requireGone(t *testing.T, pid int) {
	t.Helper()

	// A zombie still answers signal 0, so ESRCH means the process is gone for good.
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, testTimeout, 10*time.Millisecond, "process %d is still around", pid)
}

func TestStartRunsPhpspyOnThePidAtTheRate(t *testing.T) {
	t.Parallel()

	argsFile := filepath.Join(t.TempDir(), "args")
	cfg := config(script(t, `printf '%s\n' "$@" > "`+argsFile+`"`+"\n"))
	cfg.Args = []string{"--max-depth=-1", "-c"}

	a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 1))
	require.NoError(t, err)
	require.Equal(t, attach.Result{Outcome: attach.Exited}, awaitResult(t, a))

	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, "-p\n4242\n-H\n25\n--max-depth=-1\n-c\n", string(args))
}

func TestWaitDeliversTheOutputPrintedBeforeExit(t *testing.T) {
	t.Parallel()

	samples := make(chan *collector.Sample, 8)
	a, err := attach.Start(context.Background(), config(script(t, "printf '"+traceBlock+"'\n")), samples)
	require.NoError(t, err)

	require.Equal(t, attach.Result{Outcome: attach.Exited}, awaitResult(t, a))

	collected := drain(samples)
	require.Len(t, collected, 1, "the block written right before phpspy exited must still be parsed")
	require.Equal(t, `main /srv/app/public/index.php;App\Kernel::handle`, collected[0].Trace)
	require.Equal(t, "uri=/orders/42", collected[0].Tags)
	require.Equal(t, sampleRate, collected[0].SampleRate, "every sample carries the attach's rate")
}

func TestDetachEndsPhpspyAndKeepsWhatItWrote(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "pid")
	samples := make(chan *collector.Sample, 8)
	a, err := attach.Start(context.Background(), config(script(t, "echo $$ > "+pidFile+"\nprintf '"+traceBlock+"'\nexec sleep 60\n")), samples)
	require.NoError(t, err)

	pid := pidOf(t, pidFile)
	require.Eventually(t, func() bool { return len(samples) == 1 }, testTimeout, 10*time.Millisecond, "the block printed before the detach never arrived")

	a.Detach()
	require.Equal(t, attach.Result{Outcome: attach.Detached}, awaitResult(t, a))
	requireGone(t, pid)
}

func TestWaitLeavesNoProcessBehind(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		body   string
		detach bool
	}{
		{
			name: "phpspy exits while its helper holds stdout",
			body: "sleep 60 2>/dev/null &\necho $! > \"$5\"\n",
		},
		{
			name: "phpspy exits while its helper holds stderr",
			body: "sleep 60 >/dev/null &\necho $! > \"$5\"\n",
		},
		{
			name:   "phpspy is detached while its helper runs",
			body:   "sleep 60 &\necho $! > \"$5\"\nwait\n",
			detach: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The pid file arrives as the first argument after -p <pid> -H <rate>.
			pidFile := filepath.Join(t.TempDir(), "helper")
			cfg := config(script(t, tc.body))
			cfg.Args = []string{pidFile}

			a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 8))
			require.NoError(t, err)
			helper := pidOf(t, pidFile)
			t.Cleanup(func() { _ = syscall.Kill(helper, syscall.SIGKILL) })

			if tc.detach {
				a.Detach()
			}

			awaitResult(t, a)
			requireGone(t, helper)
		})
	}
}

func TestWaitClassifiesTheExit(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		body        string
		wantOutcome attach.Outcome
		wantErr     string
	}{
		{
			name:        "exit 0 is the traced process ending",
			body:        "exit 0\n",
			wantOutcome: attach.Exited,
		},
		{
			name:        "exit 1 is a failed attach",
			body:        "echo 'phpspy: cannot attach' >&2\nexit 1\n",
			wantOutcome: attach.Failed,
			wantErr:     "exit status 1",
		},
		{
			name:        "a death by signal is a failed attach",
			body:        "kill -9 $$\n",
			wantOutcome: attach.Failed,
			wantErr:     "signal: killed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := attach.Start(context.Background(), config(script(t, tc.body)), make(chan *collector.Sample, 8))
			require.NoError(t, err)

			result := awaitResult(t, a)
			require.Equal(t, tc.wantOutcome, result.Outcome)
			if tc.wantErr == "" {
				require.NoError(t, result.Err)
			} else {
				require.ErrorContains(t, result.Err, tc.wantErr)
			}
		})
	}
}

// deniedScript prints one memory-read failure per sample it cannot take, the way phpspy does
// on a process it may not trace, and never exits by itself.
func deniedScript(pidFile string) string {
	return "echo $$ > " + pidFile + "\nwhile true; do echo 'copy_proc_mem: Failed to read memory: Operation not permitted' >&2; sleep 0.01; done\n"
}

func TestWaitKillsAnAttachThatIsSilentlyDenied(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "pid")
	cfg := config(script(t, deniedScript(pidFile)))
	cfg.SilenceTimeout = 200 * time.Millisecond

	a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 8))
	require.NoError(t, err)
	pid := pidOf(t, pidFile)

	result := awaitResult(t, a)
	require.Equal(t, attach.Failed, result.Outcome)
	require.ErrorContains(t, result.Err, "copy_proc_mem")
	requireGone(t, pid)
}

func TestDetachOfADeniedAttachIsAFailure(t *testing.T) {
	t.Parallel()

	// Rotation and pre-emption end an attach long before the silence timeout; the denial it
	// gathered until then must still count against the process.
	pidFile := filepath.Join(t.TempDir(), "pid")
	cfg := config(script(t, deniedScript(pidFile)))
	cfg.SilenceTimeout = time.Minute

	a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 8))
	require.NoError(t, err)
	pid := pidOf(t, pidFile)
	time.Sleep(500 * time.Millisecond)

	a.Detach()
	result := awaitResult(t, a)
	require.Equal(t, attach.Failed, result.Outcome)
	require.ErrorContains(t, result.Err, "copy_proc_mem")
	requireGone(t, pid)
}

func TestDetachAfterAFewTransientFailuresIsClean(t *testing.T) {
	t.Parallel()

	// A healthy worker can hit a handful of stale-frame reads; fewer than a second's worth of
	// samples is not a denied process.
	cfg := config(script(t, "for i in 1 2 3; do echo 'copy_proc_mem: Failed to copy frame; err=Bad address' >&2; done\nexec sleep 60\n"))
	cfg.SilenceTimeout = time.Minute

	a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 8))
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)

	a.Detach()
	require.Equal(t, attach.Result{Outcome: attach.Detached}, awaitResult(t, a))
}

func TestWaitLeavesAQuietAttachAlone(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		body string
	}{
		{
			name: "no output and no errors",
			body: "exec sleep 60\n",
		},
		{
			name: "errors that are not memory reads",
			body: "echo 'phpspy: something else' >&2\nexec sleep 60\n",
		},
		{
			name: "a memory read error followed by a trace block",
			body: "echo 'copy_proc_mem: failed once' >&2\nprintf '" + traceBlock + "'\nexec sleep 60\n",
		},
		{
			name: "a trace block followed by one memory read error",
			body: "printf '" + traceBlock + "'\necho 'copy_proc_mem: failed once' >&2\nexec sleep 60\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config(script(t, tc.body))
			cfg.SilenceTimeout = 200 * time.Millisecond

			a, err := attach.Start(context.Background(), cfg, make(chan *collector.Sample, 8))
			require.NoError(t, err)

			stillRunning(t, a, 3*cfg.SilenceTimeout)

			a.Detach()
			require.Equal(t, attach.Result{Outcome: attach.Detached}, awaitResult(t, a))
		})
	}
}

func TestWaitFailsOnAnOverLongLine(t *testing.T) {
	t.Parallel()

	// 2 MB is past the stdout line cap; the script then stays alive holding the pipe.
	longLine := filepath.Join(t.TempDir(), "long-line")
	require.NoError(t, os.WriteFile(longLine, append([]byte(strings.Repeat("x", 2<<20)), '\n'), 0o644))
	pidFile := filepath.Join(t.TempDir(), "pid")

	samples := make(chan *collector.Sample, 8)
	a, err := attach.Start(context.Background(), config(script(t, "echo $$ > "+pidFile+"\nprintf '"+traceBlock+"'\ncat "+longLine+"\nexec sleep 60\n")), samples)
	require.NoError(t, err)
	pid := pidOf(t, pidFile)

	result := awaitResult(t, a)
	require.Equal(t, attach.Failed, result.Outcome)
	require.ErrorIs(t, result.Err, bufio.ErrTooLong)
	require.Len(t, drain(samples), 1, "the block parsed before the over-long line must be delivered")
	requireGone(t, pid)
}

func TestStartReportsAPhpspyThatCannotRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		executable func(t *testing.T) string
	}{
		{
			name: "missing binary",
			executable: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "phpspy")
			},
		},
		{
			name: "binary without the execute bit",
			executable: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "phpspy")
				writeExecutable(t, path, "#!/bin/sh\n")
				require.NoError(t, os.Chmod(path, 0o644))
				return path
			},
		},
		{
			name: "interpreter that does not exist",
			executable: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "phpspy")
				writeExecutable(t, path, "#!/nonexistent/sh\n")
				return path
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := attach.Start(context.Background(), config(tc.executable(t)), make(chan *collector.Sample, 1))
			require.ErrorContains(t, err, "cannot start")
		})
	}
}

func TestStartRefusesAnEndedContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := attach.Start(ctx, config(script(t, "exit 0\n")), make(chan *collector.Sample, 1))
	require.ErrorIs(t, err, context.Canceled)
}

func TestAnEndedContextAbandonsTheAttach(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Nobody reads samples, so a parser that kept going would block on the first block.
	a, err := attach.Start(ctx, config(script(t, "echo $$ > "+pidFile+"\nprintf '"+traceBlock+traceBlock+"'\nexec sleep 60\n")), make(chan *collector.Sample))
	require.NoError(t, err)
	pid := pidOf(t, pidFile)

	cancel()
	require.Equal(t, attach.Result{Outcome: attach.Detached}, awaitResult(t, a))
	requireGone(t, pid)
}
