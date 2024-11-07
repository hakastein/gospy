package pyroscope

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"gospy/internal/collector"
)

// Worker manages sending TagCollection data to the Pyroscope server.
type Worker struct {
	client    *Client
	collector *collector.TraceCollector
	limiter   *rate.Limiter
}

// NewSender initializes and returns a new Worker.
func NewSender(client *Client, collector *collector.TraceCollector, limiter *rate.Limiter) *Worker {
	return &Worker{
		limiter:   limiter,
		client:    client,
		collector: collector,
	}
}

// Start launches worker goroutines to send data to Pyroscope.
func (s *Worker) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("shutdown worker")
			return
		default:
			data := s.collector.ConsumeTag()
			if data == nil {
				// No data available. Sleep briefly to prevent tight loop.
				time.Sleep(100 * time.Millisecond)
				continue
			}

			body := data.DataToBuffer()

			// Respect rate limiting
			if err := s.limiter.WaitN(ctx, body.Len()); err != nil {
				log.Error().
					Err(err).
					Msg("Rate limiter error")
				continue
			}

			// Attempt to send the request with retries
			if err := s.client.send(body, data); err != nil {
				log.Error().
					Err(err).
					Msg("Failed to send data to Pyroscope")
				continue
			}

			log.Debug().
				Str("tags", data.Tags).
				Msg("Successfully sent data to Pyroscope")
		}
	}
}
