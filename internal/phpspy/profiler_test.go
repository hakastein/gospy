package phpspy_test

import (
	"context"
	"testing"

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
