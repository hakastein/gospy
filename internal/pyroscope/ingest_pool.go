package pyroscope

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
)

type SendResult struct {
	Bytes int
	Err   error
}

type IngestPool struct {
	processor    *Processor
	workers      int
	statsChannel chan<- *SendResult
	wg           sync.WaitGroup
}

func NewIngestPool(client *Client, appMetadata *AppMetadata, rateLimiter *rate.Limiter, workers int, statsChannel chan<- *SendResult) *IngestPool {
	processor := NewProcessor(client, appMetadata, rateLimiter)
	return &IngestPool{
		processor:    processor,
		workers:      workers,
		statsChannel: statsChannel,
	}
}

func (pool *IngestPool) Start(ctx context.Context, input <-chan *collector.TagCollection) {
	for i := 0; i < pool.workers; i++ {
		pool.wg.Add(1)
		go func() {
			defer pool.wg.Done()

			log.Info().Msg("pyroscope ingest worker started")
			defer log.Info().Msg("pyroscope ingest worker shutting down")

			for {
				select {
				case <-ctx.Done():
					return
				case profileData, ok := <-input:
					if !ok {
						return
					}

					pool.process(ctx, profileData)
				}
			}
		}()
	}
}

func (pool *IngestPool) Wait() {
	pool.wg.Wait()
}

func (pool *IngestPool) process(ctx context.Context, profileData *collector.TagCollection) {
	dataSize := profileData.Len()
	log.Debug().
		Str("tags", profileData.Tags()).
		Int("bytes", dataSize).
		Int("samples", len(profileData.Data())).
		Time("from", profileData.From()).
		Time("until", profileData.Until()).
		Msg("pyroscope ingest worker processing batch")
	err := pool.processor.ProcessData(ctx, profileData)

	if err != nil {
		log.Error().
			Err(err).
			Msg("failed to send data to Pyroscope")
	} else {
		log.Debug().
			Str("tags", profileData.Tags()).
			Msg("successfully sent data to Pyroscope")
	}

	if pool.statsChannel != nil {
		result := &SendResult{
			Bytes: dataSize,
			Err:   err,
		}
		select {
		case pool.statsChannel <- result:
		case <-ctx.Done():
		}
	}
}
