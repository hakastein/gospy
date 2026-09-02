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

func TestProfilerIsConfigurationValid(t *testing.T) {
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
			name:    "standalone long flag enables an unsupported mode",
			args:    []string{"--version"},
			wantErr: "flag -v/--version is unsupported by gospy",
		},
		{
			name:    "standalone short flag enables an unsupported mode",
			args:    []string{"-1"},
			wantErr: "flag -1/--single-line is unsupported by gospy",
		},
		{
			name: "explicitly disabled unsupported flag is accepted",
			args: []string{"--top=false"},
		},
		{
			name: "short output flag takes the next argument",
			args: []string{"-o", "-"},
		},
		{
			name:    "output to a file is rejected",
			args:    []string{"--output=/tmp/profile.txt"},
			wantErr: "output must be set to stdout",
		},
		{
			name:    "short output flag swallows a following flag as its value",
			args:    []string{"-o", "--rate-hz=250"},
			wantErr: "output must be set to stdout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := phpspy.NewProfiler("phpspy", tc.args).IsConfigurationValid()

			if tc.wantErr == "" {
				require.NoError(t, err)
				require.True(t, valid)
				return
			}

			require.EqualError(t, err, tc.wantErr)
			require.False(t, valid)
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
			name: "phpspy default when the flag is absent",
			args: nil,
			want: 99,
		},
		{
			name: "long flag with an inline value",
			args: []string{"--rate-hz=250"},
			want: 250,
		},
		{
			name: "short flag with a separate value",
			args: []string{"-H", "250"},
			want: 250,
		},
		{
			name: "non-numeric value yields the zero rate",
			args: []string{"--rate-hz=fast"},
			want: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, phpspy.NewProfiler("phpspy", tc.args).GetHZ())
		})
	}
}
