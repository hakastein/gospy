package phpspy_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/phpspy"
)

func TestProfilerStartExposesStderrScanner(t *testing.T) {
	profiler := phpspy.NewProfiler("sh", []string{"-c", "echo profiler-stderr >&2"})

	stdoutScanner, stderrScanner, err := profiler.Start(context.Background())
	require.NoError(t, err)

	require.False(t, stdoutScanner.Scan())
	require.True(t, stderrScanner.Scan())
	require.Equal(t, "profiler-stderr", stderrScanner.Text())

	require.NoError(t, profiler.Wait())
}

func TestProfilerOutputOutlivesTheProfiler(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	profiler := phpspy.NewProfiler("sh", []string{"-c", `echo trace; echo failure >&2; echo $$ > "$1"`, "sh", pidFile})

	stdout, stderr, err := profiler.Start(context.Background())
	require.NoError(t, err)

	// A zombie still answers signal 0, so ESRCH means the profiler is gone for good.
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(pidFile)
		if err != nil || !bytes.HasSuffix(content, []byte("\n")) {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
		return err == nil && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 5*time.Second, 10*time.Millisecond, "the profiler did not go away on its own")

	require.True(t, stdout.Scan())
	require.Equal(t, "trace", stdout.Text())
	require.False(t, stdout.Scan())
	require.NoError(t, stdout.Err())

	require.True(t, stderr.Scan())
	require.Equal(t, "failure", stderr.Text())

	require.NoError(t, profiler.Wait())
}

func TestProfilerLeavesNoProcessBehind(t *testing.T) {
	testCases := []struct {
		name   string
		script string
		cancel bool
	}{
		{
			name:   "the profiler exits on its own",
			script: "sleep 60 >/dev/null 2>&1 &\necho $!\n",
		},
		{
			name:   "the profiler exits while its child holds stdout",
			script: "sleep 60 2>/dev/null &\necho $!\n",
		},
		{
			name:   "the profiler exits while its child holds stderr",
			script: "sleep 60 >/dev/null &\necho $!\n",
		},
		{
			name:   "the profiler is cancelled",
			script: "sleep 60 &\necho $!\nwait\n",
			cancel: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			profiler := phpspy.NewProfiler("sh", []string{"-c", tc.script})
			stdout, stderr, err := profiler.Start(ctx)
			require.NoError(t, err)

			require.True(t, stdout.Scan())
			child, err := strconv.Atoi(stdout.Text())
			require.NoError(t, err)
			t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

			if tc.cancel {
				cancel()
			}

			// A child left holding the output keeps it open until the whole group is signaled.
			drained := make(chan struct{})
			go func() {
				defer close(drained)
				for stdout.Scan() {
				}
				for stderr.Scan() {
				}
			}()
			select {
			case <-drained:
			case <-time.After(10 * time.Second):
				t.Fatal("the profiler's child still holds its output")
			}

			_ = profiler.Wait()

			require.Eventually(t, func() bool {
				return errors.Is(syscall.Kill(child, 0), syscall.ESRCH)
			}, 5*time.Second, 10*time.Millisecond, "the profiler's child outlived it")
		})
	}
}

func TestProfilerValidateConfiguration(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name: "no arguments",
			args: nil,
		},
		{
			name:    "long switch enables an unsupported mode",
			args:    []string{"--version"},
			wantErr: "flag -v/--version is unsupported by gospy",
		},
		{
			name:    "short switch enables an unsupported mode",
			args:    []string{"-1"},
			wantErr: "flag -1/--single-line is unsupported by gospy",
		},
		{
			name:    "a switch is not swallowed by the argument after it",
			args:    []string{"-v", "--rate-hz=250"},
			wantErr: "flag -v/--version is unsupported by gospy",
		},
		{
			name:    "clustered switches are read one by one",
			args:    []string{"-1t"},
			wantErr: "flag -t/--top is unsupported by gospy",
		},
		{
			name:    "a switch written with a value is still a switch",
			args:    []string{"--top=false"},
			wantErr: "flag -t/--top is unsupported by gospy",
		},
		{
			name: "short output flag takes the next argument",
			args: []string{"-o", "-"},
		},
		{
			name: "long output flag takes the next argument",
			args: []string{"--output", "-"},
		},
		{
			name:    "the literal stdout is a file path to phpspy",
			args:    []string{"-o", "stdout"},
			wantErr: "phpspy must write to stdout: pass `-o -` or omit the flag, got \"stdout\"",
		},
		{
			name:    "output to a file is rejected",
			args:    []string{"--output=/tmp/profile.txt"},
			wantErr: "phpspy must write to stdout: pass `-o -` or omit the flag, got \"/tmp/profile.txt\"",
		},
		{
			name:    "output flag without a value is rejected",
			args:    []string{"-o"},
			wantErr: "phpspy must write to stdout: pass `-o -` or omit the flag, got \"\"",
		},
		{
			name: "the default event handler is accepted",
			args: []string{"-j", "fout"},
		},
		{
			name:    "another event handler changes the output format",
			args:    []string{"--event-handler=callgrind"},
			wantErr: `event handler "callgrind" is unsupported by gospy, expected fout`,
		},
		{
			name: "an option value that looks like a switch is not read as one",
			args: []string{"-f", "-v"},
		},
		{
			name: "a sleep interval of one second is the slowest rate",
			args: []string{"-s", "1000000000"},
		},
		{
			name:    "a sleep interval above one second leaves no whole sample per second",
			args:    []string{"--sleep-ns=1500000000"},
			wantErr: "sleep interval 1500000000 ns is longer than one second: Pyroscope needs a sample rate of at least 1 Hz",
		},
		{
			name: "arguments of the traced command are not phpspy flags",
			args: []string{"-p", "123", "--", "php", "-v"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := phpspy.NewProfiler("phpspy", tc.args).ValidateConfiguration()

			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestProfilerGetHZ(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "phpspy default when no rate is given",
			args: nil,
			want: 99,
		},
		{
			name: "long flag with an inline value",
			args: []string{"--rate-hz=250"},
			want: 250,
		},
		{
			name: "long flag with a separate value",
			args: []string{"--rate-hz", "250"},
			want: 250,
		},
		{
			name: "short flag with a separate value",
			args: []string{"-H", "250"},
			want: 250,
		},
		{
			name: "short flag with an attached value",
			args: []string{"-H250"},
			want: 250,
		},
		{
			name: "sleep interval sets the same rate",
			args: []string{"-s", "5000000"},
			want: 200,
		},
		{
			name: "long sleep interval sets the same rate",
			args: []string{"--sleep-ns=4000000"},
			want: 250,
		},
		{
			name: "the last of rate and sleep wins",
			args: []string{"-H", "99", "-s", "5000000"},
			want: 200,
		},
		{
			name: "the last of sleep and rate wins",
			args: []string{"-s", "5000000", "-H", "250"},
			want: 250,
		},
		{
			name: "non-numeric value falls back to the default",
			args: []string{"--rate-hz=fast"},
			want: 99,
		},
		{
			name: "a rate given to the traced command is not phpspy's",
			args: []string{"-p", "123", "--", "php", "-H", "250"},
			want: 99,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, phpspy.NewProfiler("phpspy", tc.args).GetHZ())
		})
	}
}
