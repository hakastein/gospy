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

// Worker manages sending TagCollection data to the Pyroscope server.
type Worker struct {
	pyroscopeClient *Client
	app             *AppData
	collector       *collector.TraceCollector
	limiter         *rate.Limiter
	statsChan       chan<- *RequestStats
}

// NewWorker initializes and returns a new Worker with a statistics channel.
func NewWorker(pyroscopeClient *Client, app *AppData, collector *collector.TraceCollector, limiter *rate.Limiter, statsChan chan<- *RequestStats) *Worker {
	return &Worker{
		limiter:         limiter,
		pyroscopeClient: pyroscopeClient,
		app:             app,
		collector:       collector,
		statsChan:       statsChan,
	}
}

// Start a goroutine to Send data to Pyroscope.
func (s *Worker) Start(ctx context.Context) {
	go func() {
		log.Info().Msg("pyroscope worker started")
		var (
			data    *collector.TagCollection
			bodyLen int
			ok      bool
		)
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("pyroscope worker shutting down")
				return
			default:
				data, ok = s.collector.ConsumeTag()
				if ok == false {
					// No data available. Sleep briefly.
					time.Sleep(100 * time.Millisecond)
					continue
				}

				bodyLen = data.Len()

				// Respect rate limiting
				if err := s.limiter.WaitN(ctx, bodyLen); err != nil {
					log.Error().
						Err(err).
						Msg("rate limiter error")
					continue
				}

				ingestData := s.app.IngestData(data)

				// Attempt to Send the request
				statusCode, err := s.pyroscopeClient.Send(ctx, ingestData)
				if err != nil {
					// @TODO make retry for certain type of errors
					s.statsChan <- &RequestStats{
						Bytes:      bodyLen,
						StatusCode: statusCode,
						Success:    false,
					}
					log.Error().
						Err(err).
						Int("status_code", statusCode).
						Msg("failed to Send data to Pyroscope")
					continue
				}

				s.statsChan <- &RequestStats{
					Bytes:      bodyLen,
					StatusCode: statusCode,
					Success:    true,
				}
				log.Debug().
					Str("tags", data.Tags()).
					Int("status_code", statusCode).
					Msg("successfully sent data to Pyroscope")
			}
		}
	}()
}
