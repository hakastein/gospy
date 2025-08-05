package pyroscope

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StartStatsAggregator starts a goroutine to aggregate and log statistics every interval.
// StartStatsAggregator periodically reports statistics about Pyroscope data transfer
// including total requests, bytes sent, success/failure counts, and status codes.
func StartStatsAggregator(ctx context.Context, statsChan <-chan *RequestStats, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
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
			case stat, ok := <-statsChan:
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
	}()
}
