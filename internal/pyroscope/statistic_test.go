package pyroscope_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/stretchr/testify/require"
)

func TestStatsAggregatorCountsOnlyRealErrors(t *testing.T) {
	t.Parallel()

	statsChan := make(chan *pyroscope.SendResult, 2)
	statsChan <- &pyroscope.SendResult{Bytes: 10}
	statsChan <- &pyroscope.SendResult{Bytes: 5, Err: errors.New("boom")}
	close(statsChan)

	aggregator := pyroscope.NewStatsAggregator(statsChan, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aggregator.Start(ctx)
	report := <-aggregator.Reports()
	aggregator.Wait()

	require.Equal(t, 2, report.TotalRequests)
	require.Equal(t, 15, report.TotalBytes)
	require.Equal(t, 1, report.SuccessRequests)
	require.Equal(t, 1, report.FailedRequests)
	require.Equal(t, map[string]int{"boom": 1}, report.Errors)
}
