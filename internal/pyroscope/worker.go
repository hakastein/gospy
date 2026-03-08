package pyroscope

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
)

const defaultPollInterval = 100 * time.Millisecond

// RequestStats represents the statistics of a single request.
type RequestStats struct {
	Bytes   int
	Success bool
	Error   error
}

// Worker manages to send profile data to the Pyroscope server.
type Worker struct {
	processor    *Processor
	collector    *collector.TraceCollector
	statsChannel chan<- *RequestStats
	done         chan struct{}
	wg           sync.WaitGroup
}

// NewWorker initializes and returns a new Worker with a statistics channel.
func NewWorker(client *Client, appMetadata *AppMetadata, collector *collector.TraceCollector, rateLimiter *rate.Limiter, statsChannel chan<- *RequestStats) *Worker {
	processor := NewProcessor(client, appMetadata, rateLimiter)
	return &Worker{
		processor:    processor,
		collector:    collector,
		statsChannel: statsChannel,
		done:         make(chan struct{}),
	}
}

// Start launches a goroutine to send profile data to Pyroscope server.
func (worker *Worker) Start(ctx context.Context) {
	worker.wg.Add(1)
	go func() {
		defer worker.wg.Done()
		defer close(worker.done)

		log.Info().Msg("pyroscope worker started")

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("pyroscope worker shutting down")
				return
			default:
				if !worker.processNext(ctx) {
					time.Sleep(defaultPollInterval)
				}
			}
		}
	}()
}

// processNext handles one iteration - simple coordinator logic
func (worker *Worker) processNext(ctx context.Context) bool {
	profileData, ok := worker.collector.ConsumeTag()
	if !ok {
		return false
	}

	dataSize := profileData.Len()
	err := worker.processor.ProcessData(ctx, profileData)

	// Log the results
	if err != nil {
		log.Error().
			Err(err).
			Msg("failed to send data to Pyroscope")
	} else {
		log.Debug().
			Str("tags", profileData.Tags()).
			Msg("successfully sent data to Pyroscope")
	}

	// Create and send statistics
	stats := &RequestStats{
		Bytes:   dataSize,
		Success: err == nil,
		Error:   err,
	}
	worker.statsChannel <- stats
	return true
}
