package pyroscope

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestStatsAggregatorCountsOnlyRealErrors(t *testing.T) {
	var output bytes.Buffer
	previousLogger := log.Logger
	log.Logger = zerolog.New(&output)
	defer func() {
		log.Logger = previousLogger
	}()

	statsChan := make(chan *RequestStats, 2)
	statsChan <- &RequestStats{Bytes: 10, Success: true}
	statsChan <- &RequestStats{Bytes: 5, Success: false, Error: errors.New("boom")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aggregator := NewStatsAggregator(statsChan, 10*time.Millisecond)
	aggregator.Start(ctx)

	time.Sleep(30 * time.Millisecond)
	cancel()

	require.Contains(t, output.String(), "\"failed_requests\":1")
	require.Contains(t, output.String(), "\"success_requests\":1")
	require.Contains(t, output.String(), "\"errors\":{\"boom\":1}")
	require.NotContains(t, strings.ReplaceAll(output.String(), " ", ""), "<nil>")
}
