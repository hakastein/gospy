package pyroscope

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
)

const (
	bytesPerMegabyte = 1048576
	defaultTimeout   = 10 * time.Second
)

// Config: a nil Transport keeps the real one, Logger takes the module's logs and gates
// statistics, Workers below one becomes one, and a Timeout at or below zero becomes ten seconds.
// A URL that does not parse is reported as a failed send, never silently dropped.
type Config struct {
	URL, AuthToken, AppName, StaticTags string
	SampleRate, Workers                 int
	Timeout                             time.Duration
	RateMB, RateBurstMB                 float64
	StatsInterval                       time.Duration
	Logger                              zerolog.Logger
	Transport                           http.RoundTripper
}

// Ingest failures are logged and counted, never returned.
type Ingest struct {
	input    chan *collector.TagCollection
	client   *client
	metadata *appMetadata
	limiter  *rate.Limiter
	stats    *statistics
	logger   zerolog.Logger
	workers  sync.WaitGroup
}

// StartIngest expects its producer to feed batches into In and close it exactly once;
// cancelling ctx drops whatever is still in flight.
func StartIngest(ctx context.Context, cfg Config) *Ingest {
	workers := max(cfg.Workers, 1)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	ingest := &Ingest{
		input:    make(chan *collector.TagCollection, workers),
		client:   newClient(cfg.URL, cfg.AuthToken, timeout, cfg.Transport, cfg.Logger),
		metadata: newAppMetadata(cfg.AppName, cfg.StaticTags, cfg.SampleRate),
		limiter:  rate.NewLimiter(rate.Limit(megabytesToBytes(cfg.RateMB)), megabytesToBytes(cfg.RateBurstMB)),
		logger:   cfg.Logger,
	}

	if cfg.StatsInterval > 0 && cfg.Logger.Info().Enabled() {
		ingest.stats = startStatistics(ctx, cfg.StatsInterval, cfg.Logger)
	}

	for i := 0; i < workers; i++ {
		ingest.workers.Add(1)
		go ingest.work(ctx)
	}

	return ingest
}

func (ingest *Ingest) In() chan<- *collector.TagCollection {
	return ingest.input
}

// Wait returns only once the producer has closed In: it blocks until every accepted batch is
// resolved and the final statistics report is flushed.
func (ingest *Ingest) Wait() {
	ingest.workers.Wait()
	ingest.stats.stop()
}

func (ingest *Ingest) work(ctx context.Context) {
	defer ingest.workers.Done()

	ingest.logger.Info().Msg("pyroscope ingest worker started")
	defer ingest.logger.Info().Msg("pyroscope ingest worker shutting down")

	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-ingest.input:
			if !ok {
				return
			}

			ingest.deliver(ctx, batch)
		}
	}
}

func (ingest *Ingest) deliver(ctx context.Context, batch *collector.TagCollection) {
	profile := ingest.metadata.newPayload(batch)

	ingest.logger.Debug().
		Str("tags", batch.Tags()).
		Int("bytes", len(profile.body)).
		Int("samples", len(batch.Data())).
		Time("from", batch.From()).
		Time("until", batch.Until()).
		Msg("pyroscope ingest worker processing batch")

	err := ingest.limiter.WaitN(ctx, len(profile.body))
	if err == nil {
		err = ingest.client.send(ctx, profile)
	}

	if err != nil {
		ingest.logger.Error().
			Err(err).
			Msg("failed to send data to Pyroscope")
	} else {
		ingest.logger.Debug().
			Str("tags", batch.Tags()).
			Msg("successfully sent data to Pyroscope")
	}

	ingest.stats.record(ctx, len(profile.body), err)
}

func megabytesToBytes(megabytes float64) int {
	return int(megabytes * bytesPerMegabyte)
}
