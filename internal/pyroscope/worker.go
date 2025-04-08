package pyroscope

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
)

// RequestStats represents the statistics of a single request.
type RequestStats struct {
	Bytes      int
	StatusCode int
	Success    bool
}

// Worker manages sending profile data to the Pyroscope server.
type Worker struct {
	client       *Client
	appMetadata  *AppMetadata
	collector    *collector.TraceCollector
	rateLimiter  *rate.Limiter
	statsChannel chan<- *RequestStats
}

// NewWorker initializes and returns a new Worker with a statistics channel.
func NewWorker(client *Client, appMetadata *AppMetadata, collector *collector.TraceCollector, rateLimiter *rate.Limiter, statsChannel chan<- *RequestStats) *Worker {
	return &Worker{
		client:       client,
		appMetadata:  appMetadata,
		collector:    collector,
		rateLimiter:  rateLimiter,
		statsChannel: statsChannel,
	}
}

// Start launches a goroutine to send profile data to Pyroscope server.
// It continuously consumes data from the collector and sends it to Pyroscope
// until the context is canceled.
func (worker *Worker) Start(ctx context.Context) {
	go func() {
		log.Info().Msg("pyroscope worker started")
		var (
			profileData *collector.TagCollection
			dataSize    int
			ok          bool
		)

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("pyroscope worker shutting down")
				return
			default:
				profileData, ok = worker.collector.ConsumeTag()
				if !ok {
					// No data available. Sleep briefly.
					time.Sleep(100 * time.Millisecond)
					continue
				}

				dataSize = profileData.Len()

				// Respect rate limiting
				if err := worker.rateLimiter.WaitN(ctx, dataSize); err != nil {
					log.Error().
						Err(err).
						Msg("rate limiter error")
					continue
				}

				payload := worker.appMetadata.NewPayload(profileData)

				// Attempt to send the request
				statusCode, err := worker.client.Send(ctx, payload)
				if err != nil {
					// @TODO make retry for certain type of errors
					worker.statsChannel <- &RequestStats{
						Bytes:      dataSize,
						StatusCode: statusCode,
						Success:    false,
					}
					
					log.Error().
						Err(err).
						Int("status_code", statusCode).
						Msg("failed to send data to Pyroscope")
					continue
				}

				worker.statsChannel <- &RequestStats{
					Bytes:      dataSize,
					StatusCode: statusCode,
					Success:    true,
				}
				
				log.Debug().
					Str("tags", profileData.Tags()).
					Int("status_code", statusCode).
					Msg("successfully sent data to Pyroscope")
			}
		}
	}()
}