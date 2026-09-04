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
// A RateMB at or below zero is unlimited, and RateBurstMB only paces how fast a batch is paid
// for, never how large a batch may be. A URL that does not parse is reported as a failed send,
// never silently dropped.
type Config struct {
	URL, AuthToken, AppName, StaticTags string
	SampleRate, Workers                 int
	Timeout                             time.Duration
	RateMB, RateBurstMB                 float64
	Retry                               Retry
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
	retry    retryPolicy
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
		limiter:  newLimiter(cfg.RateMB, cfg.RateBurstMB),
		retry:    newRetryPolicy(cfg.Retry),
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

	attempts, err := ingest.send(ctx, profile)

	if err != nil {
		ingest.logger.Error().
			Err(err).
			Int("attempts", attempts).
			Msg("failed to send data to Pyroscope")
	} else {
		ingest.logger.Debug().
			Str("tags", batch.Tags()).
			Int("attempts", attempts).
			Msg("successfully sent data to Pyroscope")
	}

	ingest.stats.record(ctx, sendResult{bytes: len(profile.body), retries: attempts - 1, err: err})
}

// send offers the batch again while the refusal looks transient, and returns how many attempts
// that took: the caller counts them, so a batch that took four tries is not read as four batches.
func (ingest *Ingest) send(ctx context.Context, profile payload) (int, error) {
	var err error

	for attempt := 1; ; attempt++ {
		if err = ingest.pace(ctx, len(profile.body)); err != nil {
			return attempt, err
		}

		if err = ingest.client.send(ctx, profile); err == nil {
			return attempt, nil
		}

		if attempt >= ingest.retry.attempts || !retryable(err) || ctx.Err() != nil {
			return attempt, err
		}

		delay := ingest.retry.delay(attempt, err, time.Now())

		ingest.logger.Debug().
			Err(err).
			Int("attempt", attempt).
			Dur("delay", delay).
			Msg("retrying pyroscope send")

		if !wait(ctx, delay) {
			return attempt, err
		}
	}
}

// pace pays for the body one burst at a time: WaitN refuses outright anything larger than the
// burst, which would drop a batch that grew past it rather than slow it down.
func (ingest *Ingest) pace(ctx context.Context, size int) error {
	if ingest.limiter.Limit() == rate.Inf {
		return ctx.Err()
	}

	burst := max(ingest.limiter.Burst(), 1)
	for remaining := size; remaining > 0; {
		chunk := min(remaining, burst)
		if err := ingest.limiter.WaitN(ctx, chunk); err != nil {
			return err
		}

		remaining -= chunk
	}

	return ctx.Err()
}

// newLimiter reads a rate at or below zero as unlimited: rate.Limit(0) lets the first burst
// through and then blocks every later batch forever, taking shutdown down with it.
func newLimiter(rateMB, burstMB float64) *rate.Limiter {
	bytesPerSecond := megabytesToBytes(rateMB)
	if bytesPerSecond <= 0 {
		return rate.NewLimiter(rate.Inf, 1)
	}

	return rate.NewLimiter(rate.Limit(bytesPerSecond), max(megabytesToBytes(burstMB), 1))
}

func megabytesToBytes(megabytes float64) int {
	return int(megabytes * bytesPerMegabyte)
}
