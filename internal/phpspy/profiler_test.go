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
