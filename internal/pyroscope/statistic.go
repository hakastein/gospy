package pyroscope

import (
	"context"
	"github.com/rs/zerolog/log"
	"time"
)

// StartStatsAggregator starts a goroutine to aggregate and log statistics every interval.
func StartStatsAggregator(ctx context.Context, statsChan <-chan *RequestStats, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var (
			totalRequests   int
			totalBytes      int
			successRequests int
			failedRequests  int
			statusCodes     = make(map[int]int)
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
				statusCodes[stat.StatusCode]++
			case <-ticker.C:
				if totalRequests > 0 {
					log.Info().
						Int("total_requests", totalRequests).
						Int("total_bytes", totalBytes).
						Int("success_requests", successRequests).
						Int("failed_requests", failedRequests).
						Interface("status_codes", statusCodes).
						Msg("pyroscope sending statistics")

					// Reset statistics
					totalRequests, totalBytes, successRequests, failedRequests = 0, 0, 0, 0
					statusCodes = make(map[int]int)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
