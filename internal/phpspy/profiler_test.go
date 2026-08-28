package phpspy_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/phpspy"
)

func TestProfilerLogsStderrOutput(t *testing.T) {
	var output bytes.Buffer
	previousLogger := log.Logger
	previousLevel := zerolog.GlobalLevel()
	log.Logger = zerolog.New(&output)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defer func() {
		log.Logger = previousLogger
		zerolog.SetGlobalLevel(previousLevel)
	}()

	profiler := phpspy.NewProfiler("sh", []string{"-c", "echo profiler-stderr >&2"})

	scanner, err := profiler.Start(context.Background())
	require.NoError(t, err)

	require.False(t, scanner.Scan())

	require.NoError(t, profiler.Wait())
	require.Contains(t, output.String(), "profiler stderr")
	require.Contains(t, output.String(), "profiler-stderr")
}
