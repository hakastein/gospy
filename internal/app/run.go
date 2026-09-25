package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/version"
)

const (
	sampleBuffer         = 1000
	DefaultBatchInterval = 5 * time.Second
	DefaultDrainTimeout  = 10 * time.Second
)

var ErrDrainAborted = errors.New("shutdown drain aborted by a second signal")

// Config: a nil Transport keeps the real one; a DrainTimeout at or below zero takes DefaultDrainTimeout;
// an empty Restart never restarts.
type Config struct {
	PyroscopeURL       string
	PyroscopeAuth      string
	PyroscopeWorkers   int
	PyroscopeTimeout   time.Duration
	TagEntrypoint      bool
	KeepEntrypointName bool
	AppName            string
	Restart            string
	RateMB             float64
	RateBurstMB        float64
	PyroscopeRetry     pyroscope.Retry
	AppTags            []string
	Entrypoints        []string
	BatchInterval      time.Duration
	StatsInterval      time.Duration
	DrainTimeout       time.Duration
	ProfilerApp        string
	ProfilerArguments  []string
	Transport          http.RoundTripper
}

type runtimeConfig struct {
	Config
	staticTags  string
	dynamicTags map[string][]tag.DynamicTag
}

type profilerRunner interface {
	Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error)
	Wait() error
	ValidateConfiguration() error
	GetHZ() int
}

type traceParser interface {
	Parse(ctx context.Context, scanner *bufio.Scanner, samplesChannel chan<- *collector.Sample) error
}

func Run(ctx context.Context, cfg Config) error {
	runtimeCfg, err := prepareConfig(cfg)
	if err != nil {
		return err
	}

	profilerImpl, parserImpl, err := newSource(runtimeCfg)
	if err != nil {
		return err
	}

	if validationErr := profilerImpl.ValidateConfiguration(); validationErr != nil {
		return validationErr
	}

	return runPipeline(ctx, runtimeCfg, profilerImpl, parserImpl)
}

func prepareConfig(cfg Config) (runtimeConfig, error) {
	if err := validateConfig(cfg); err != nil {
		return runtimeConfig{}, err
	}

	staticTags, dynamicTags, err := tag.ParseInput(cfg.AppTags)
	if err != nil {
		return runtimeConfig{}, err
	}

	return runtimeConfig{
		Config:      cfg,
		staticTags:  staticTags,
		dynamicTags: dynamicTags,
	}, nil
}

func newSource(cfg runtimeConfig) (profilerRunner, traceParser, error) {
	switch filepath.Base(cfg.ProfilerApp) {
	case "phpspy":
		return phpspy.NewProfiler(cfg.ProfilerApp, cfg.ProfilerArguments), phpspy.NewParser(cfg.Entrypoints, cfg.dynamicTags, cfg.TagEntrypoint, cfg.KeepEntrypointName), nil
	default:
		return nil, nil, fmt.Errorf("unsupported profiler: %s", cfg.ProfilerApp)
	}
}

func validateConfig(cfg Config) error {
	if cfg.ProfilerApp == "" {
		return errors.New("no profiler application specified")
	}

	if cfg.AppName == "" {
		return errors.New("no app name specified")
	}

	if cfg.Restart != "" {
		if err := supervisor.ValidateRestart(cfg.Restart); err != nil {
			return err
		}
	}

	if cfg.PyroscopeWorkers < 1 {
		return fmt.Errorf("pyroscope workers must be at least 1, got %d", cfg.PyroscopeWorkers)
	}

	pyroscopeURL, err := url.Parse(cfg.PyroscopeURL)
	if err != nil {
		return fmt.Errorf("invalid pyroscope url %q: %w", cfg.PyroscopeURL, err)
	}

	if pyroscopeURL.Scheme != "http" && pyroscopeURL.Scheme != "https" {
		return fmt.Errorf("pyroscope url must be http or https, got %q", cfg.PyroscopeURL)
	}

	if cfg.RateMB < 0 {
		return fmt.Errorf("pyroscope rate limit must not be negative, got %v MB", cfg.RateMB)
	}

	if cfg.RateBurstMB < 0 {
		return fmt.Errorf("pyroscope rate limit burst must not be negative, got %v MB", cfg.RateBurstMB)
	}

	if cfg.RateMB > 0 && cfg.RateBurstMB == 0 {
		return errors.New("pyroscope rate limit burst must be above zero when a rate limit is set")
	}

	if pyroscopeURL.Scheme == "http" && cfg.PyroscopeAuth != "" {
		log.Warn().
			Str("pyroscope_url", cfg.PyroscopeURL).
			Msg("pyroscope url is plain http, the authentication token travels in cleartext")
	}

	return nil
}

func runPipeline(
	ctx context.Context,
	cfg runtimeConfig,
	profilerImpl profilerRunner,
	parserImpl traceParser,
) error {
	log.Info().
		Str("pyroscope_url", cfg.PyroscopeURL).
		Str("pyroscope_auth", maskedToken(cfg.PyroscopeAuth)).
		Str("app_name", cfg.AppName).
		Bool("tag_entrypoint", cfg.TagEntrypoint).
		Bool("keep_entrypoint_name", cfg.KeepEntrypointName).
		Str("restart", cfg.Restart).
		Float64("rate_mb", cfg.RateMB).
		Float64("rate_burst_mb", cfg.RateBurstMB).
		Str("version", version.Get()).
		Strs("tags", cfg.AppTags).
		Msg("gospy started")

	profilerCtx, stopProfiler := context.WithCancel(ctx)
	defer stopProfiler()

	drainCtx, abandonDrain := context.WithCancel(context.WithoutCancel(ctx))
	defer abandonDrain()

	aborted, stopSignals := forwardSignals(profilerCtx, stopProfiler)
	defer stopSignals()

	stacks := make(chan *collector.Sample, sampleBuffer)
	ingest := pyroscope.StartIngest(drainCtx, cfg.ingestConfig(profilerImpl.GetHZ()))

	batchTicker := time.NewTicker(cfg.batchInterval())
	defer batchTicker.Stop()

	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		collector.Collect(drainCtx, stacks, ingest.In(), collector.Config{
			Ticks:  batchTicker.C,
			OnDrop: ingest.CountDropped,
		})
	}()

	runErr := supervisor.ManageProfiler(profilerCtx, profilerImpl, parserImpl, stacks, supervisor.RestartPolicy{Mode: cfg.Restart})

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		close(stacks)
		<-collectorDone
		ingest.Wait()
	}()

	if drainErr := awaitDrain(drained, aborted, abandonDrain, cfg.drainTimeout()); drainErr != nil {
		return errors.Join(runErr, drainErr)
	}

	log.Info().Msg("shutting down")
	return runErr
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

func (cfg runtimeConfig) batchInterval() time.Duration {
	if cfg.BatchInterval <= 0 {
		return DefaultBatchInterval
	}

	return cfg.BatchInterval
}

func (cfg runtimeConfig) drainTimeout() time.Duration {
	if cfg.DrainTimeout <= 0 {
		return DefaultDrainTimeout
	}

	return cfg.DrainTimeout
}

func (cfg runtimeConfig) ingestConfig(sampleRate int) pyroscope.Config {
	return pyroscope.Config{
		URL:           cfg.PyroscopeURL,
		AuthToken:     cfg.PyroscopeAuth,
		AppName:       cfg.AppName,
		StaticTags:    cfg.staticTags,
		SampleRate:    sampleRate,
		Workers:       cfg.PyroscopeWorkers,
		Timeout:       cfg.PyroscopeTimeout,
		RateMB:        cfg.RateMB,
		RateBurstMB:   cfg.RateBurstMB,
		Retry:         cfg.PyroscopeRetry,
		StatsInterval: cfg.StatsInterval,
		Logger:        log.Logger,
		Transport:     cfg.Transport,
	}
}

func forwardSignals(profilerCtx context.Context, stopProfiler context.CancelFunc) (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	aborted := make(chan struct{})
	done := make(chan struct{})

	go func() {
		for {
			select {
			case sig := <-signals:
				log.Info().Str("signal", sig.String()).Msg("signal received")
				if profilerCtx.Err() == nil {
					stopProfiler()
					continue
				}

				close(aborted)
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
