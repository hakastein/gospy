// Package app is the gospy runtime: it takes a loaded configuration, discovers the processes
// of every target, runs one phpspy per attach and ships what they see through one collector
// and one ingest.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/procscan"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/version"
)

const sampleBuffer = 1000

// ErrDrainAborted is returned when a second signal cuts the shutdown drain short.
var ErrDrainAborted = errors.New("shutdown drain aborted by a second signal")

// ProcessSource is where a Scan reads the process table from.
type ProcessSource interface {
	Scan() ([]procscan.Process, error)
}

// Config is the loaded configuration plus the seams tests use: a nil Transport keeps the real
// one, a nil Processes reads the live procfs, and a SilentAttachTimeout at or below zero takes
// the attach package's default.
type Config struct {
	config.Config
	Transport           http.RoundTripper
	Processes           ProcessSource
	SilentAttachTimeout time.Duration
}

// Run profiles until ctx ends or a stop signal arrives, then drains what it holds. It returns
// an error when phpspy cannot be started at all, or when the drain is aborted.
func Run(ctx context.Context, cfg Config) error {
	executable, err := exec.LookPath(cfg.Phpspy)
	if err != nil {
		return fmt.Errorf("phpspy cannot be started: %w", err)
	}

	log.Info().
		Str("pyroscope_url", cfg.Pyroscope.URL).
		Str("pyroscope_auth", maskedToken(cfg.Pyroscope.Auth)).
		Str("app_name", cfg.App).
		Str("phpspy", executable).
		Int("targets", len(cfg.Targets)).
		Str("version", version.Get()).
		Msg("gospy started")

	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	drainCtx, abandonDrain := context.WithCancel(context.WithoutCancel(ctx))
	defer abandonDrain()

	aborted, stopSignals := forwardSignals(runCtx, stop)
	defer stopSignals()

	samples := make(chan *collector.Sample, sampleBuffer)
	ingest := pyroscope.StartIngest(drainCtx, cfg.ingestConfig())

	batchTicker := time.NewTicker(cfg.batchInterval())
	defer batchTicker.Stop()

	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		collector.Collect(drainCtx, samples, ingest.In(), collector.Config{
			Ticks:  batchTicker.C,
			OnDrop: ingest.CountDropped,
		})
	}()

	profiling := newProfiling(cfg, executable, samples, drainCtx, stop)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		profiling.run(runCtx)
		close(samples)
		<-collectorDone
		ingest.Wait()
	}()

	<-runCtx.Done()
	log.Info().Msg("shutting down")

	drainErr := awaitDrain(drained, aborted, abandonDrain, cfg.drainTimeout())

	return errors.Join(profiling.fatal(), drainErr)
}

// awaitDrain relies on the ingest fast-fail contract: once the drain context is cancelled the
// batch in flight fails and every queued one is counted as dropped, so the drain ends promptly.
func awaitDrain(drained, aborted <-chan struct{}, abandonDrain context.CancelFunc, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case <-drained:
		return nil
	case <-aborted:
	case <-deadline.C:
		abandonDrain()
		log.Warn().Dur("drain_timeout", timeout).Msg("shutdown drain cut short, undelivered batches dropped")

		select {
		case <-drained:
			return nil
		case <-aborted:
		}
	}

	abandonDrain()
	log.Warn().Msg("shutdown drain aborted by a second signal")
	return ErrDrainAborted
}

func maskedToken(token string) string {
	if token == "" {
		return ""
	}

	return "***"
}

func (cfg Config) batchInterval() time.Duration {
	if cfg.BatchInterval <= 0 {
		return config.DefaultBatchInterval
	}

	return cfg.BatchInterval
}

func (cfg Config) drainTimeout() time.Duration {
	if cfg.DrainTimeout <= 0 {
		return config.DefaultDrainTimeout
	}

	return cfg.DrainTimeout
}

// Every sample carries its target's static tags already, so the ingest adds none of its own.
func (cfg Config) ingestConfig() pyroscope.Config {
	return pyroscope.Config{
		URL:           cfg.Pyroscope.URL,
		AuthToken:     cfg.Pyroscope.Auth,
		AppName:       cfg.App,
		Workers:       cfg.Pyroscope.Workers,
		Timeout:       cfg.Pyroscope.Timeout,
		RateMB:        cfg.Pyroscope.RateMB,
		RateBurstMB:   cfg.Pyroscope.RateBurstMB,
		StatsInterval: cfg.StatsInterval,
		Logger:        log.Logger,
		Transport:     cfg.Transport,
	}
}

func (cfg Config) processes() ProcessSource {
	if cfg.Processes != nil {
		return cfg.Processes
	}

	return procscan.New(procscan.DefaultRoot, cfg.ScanFields())
}

func forwardSignals(runCtx context.Context, stop context.CancelFunc) (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	aborted := make(chan struct{})
	done := make(chan struct{})

	var once sync.Once
	go func() {
		for {
			select {
			case sig := <-signals:
				log.Info().Str("signal", sig.String()).Msg("signal received")
				if runCtx.Err() == nil {
					stop()
					continue
				}

				once.Do(func() { close(aborted) })
				return
			case <-done:
				return
			}
		}
	}()

	return aborted, func() {
		signal.Stop(signals)
		close(done)
	}
}
