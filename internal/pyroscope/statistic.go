package pyroscope

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// StatsAggregator manages statistics collection and reporting
type StatsAggregator struct {
	statsChan <-chan *RequestStats
	interval  time.Duration
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewStatsAggregator creates a new statistics aggregator
func NewStatsAggregator(statsChan <-chan *RequestStats, interval time.Duration) *StatsAggregator {
	return &StatsAggregator{
		statsChan: statsChan,
		interval:  interval,
		done:      make(chan struct{}),
	}
}

// Start begins the statistics aggregation process
func (sa *StatsAggregator) Start(ctx context.Context) {
	sa.wg.Add(1)
	go func() {
		defer sa.wg.Done()
		defer close(sa.done)
		sa.run(ctx)
	}()
}

// run is the main aggregation loop - extracted for easier testing
func (sa *StatsAggregator) run(ctx context.Context) {
	ticker := time.NewTicker(sa.interval)
	defer ticker.Stop()

	var (
		totalRequests   int
		totalBytes      int
		successRequests int
		failedRequests  int
		errors          = make(map[error]int)
	)

	for {
		select {
		case stat, ok := <-sa.statsChan:
			if !ok {
				return
			}
			totalRequests++
			totalBytes += stat.Bytes
			if stat.Success {
				successRequests++
			} else {
				failedRequests++
			}
			errors[stat.Error]++
		case <-ticker.C:
			if totalRequests > 0 {
				log.Info().
					Int("total_requests", totalRequests).
					Int("total_bytes", totalBytes).
					Int("success_requests", successRequests).
					Int("failed_requests", failedRequests).
					Interface("errors", errors).
					Msg("pyroscope sending statistics")

				// Reset statistics
				totalRequests, totalBytes, successRequests, failedRequests = 0, 0, 0, 0
				errors = make(map[error]int)
			}
		case <-ctx.Done():
			return
		}
	}
}
