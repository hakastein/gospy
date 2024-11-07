package pyroscope

import (
	"context"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"gospy/internal/collector"
)

// RequestStats represents the statistics of a single request.
type RequestStats struct {
	Bytes      int
	StatusCode int
	Success    bool
}

// Worker manages sending TagCollection data to the Pyroscope server.
type Worker struct {
	client    *Client
	collector *collector.TraceCollector
	limiter   *rate.Limiter
	statsChan chan<- RequestStats
}

// NewSender initializes and returns a new Worker with a statistics channel.
func NewSender(client *Client, collector *collector.TraceCollector, limiter *rate.Limiter, statsChan chan<- RequestStats) *Worker {
	return &Worker{
		limiter:   limiter,
		client:    client,
		collector: collector,
		statsChan: statsChan,
	}
}

// Start a goroutine to send data to Pyroscope.
func (s *Worker) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("shutdown worker")
				return
			default:
				data := s.collector.ConsumeTag()
				if data == nil {
					// No data available. Sleep briefly.
					time.Sleep(100 * time.Millisecond)
					continue
				}

				body := data.DataToBuffer()
				bytesSent := body.Len() // Capture the length before sending

				// Respect rate limiting
				if err := s.limiter.WaitN(ctx, bytesSent); err != nil {
					log.Error().
						Err(err).
						Msg("Rate limiter error")
					continue
				}

				// Attempt to send the request
				statusCode, err := s.client.send(body, data)
				if err != nil {
					s.statsChan <- RequestStats{
						Bytes:      bytesSent,
						StatusCode: statusCode,
						Success:    false,
					}
					log.Error().
						Err(err).
						Int("status_code", statusCode).
						Msg("Failed to send data to Pyroscope")
					continue
				}

				s.statsChan <- RequestStats{
					Bytes:      bytesSent,
					StatusCode: statusCode,
					Success:    true,
				}
				log.Debug().
					Str("tags", data.Tags).
					Int("status_code", statusCode).
					Msg("Successfully sent data to Pyroscope")
			}
		}
	}()
}

// StartStatsAggregator starts a goroutine to aggregate and log statistics every interval.
func StartStatsAggregator(ctx context.Context, statsChan <-chan RequestStats, interval time.Duration) {
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
					// Convert statusCodes to map[string]int for logging
					statusDict := make(map[string]int)
					for code, count := range statusCodes {
						statusDict[strconv.Itoa(code)] = count
					}

					log.Info().
						Int("total_requests", totalRequests).
						Int("total_bytes", totalBytes).
						Int("success_requests", successRequests).
						Int("failed_requests", failedRequests).
						Interface("status_codes", statusDict).
						Msg("Pyroscope statistics")

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
