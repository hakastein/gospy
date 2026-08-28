package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/obfuscation"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/version"
)

const megabyte = 1048576

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
	AppTags            []string
	Entrypoints        []string
	StatsInterval      time.Duration
	ProfilerApp        string
	ProfilerArguments  []string
}

type runtimeConfig struct {
	Config
	staticTags  string
	dynamicTags map[string][]tag.DynamicTag
}

type profilerRunner interface {
	Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error)
	Wait() error
	IsConfigurationValid() (bool, error)
	GetHZ() int
}

type traceParser interface {
	Parse(ctx context.Context, scanner *bufio.Scanner, samplesChannel chan<- *collector.Sample)
}

type sampleCollection struct {
	traces        *collector.TraceCollector
	collectorDone <-chan struct{}
}

type ingestPipeline struct {
	stats         chan *pyroscope.SendResult
	aggregator    *pyroscope.StatsAggregator
	statsLogged   <-chan struct{}
	pool          *pyroscope.IngestPool
	publisherDone <-chan struct{}
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

	if supported, validationErr := profilerImpl.IsConfigurationValid(); !supported {
		return validationErr
	}

	httpClient := &http.Client{Timeout: runtimeCfg.PyroscopeTimeout}
	client := pyroscope.NewClient(runtimeCfg.PyroscopeURL, runtimeCfg.PyroscopeAuth, httpClient)

	return runPipeline(ctx, runtimeCfg.Config, runtimeCfg.staticTags, profilerImpl, parserImpl, client)
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

	if cfg.PyroscopeWorkers < 1 {
		return fmt.Errorf("pyroscope workers must be at least 1, got %d", cfg.PyroscopeWorkers)
	}

	return nil
}

func runPipeline(
	ctx context.Context,
	cfg Config,
	staticTags string,
	profilerImpl profilerRunner,
	parserImpl traceParser,
	client *pyroscope.Client,
) error {
	log.Info().
		Str("pyroscope_url", cfg.PyroscopeURL).
		Str("pyroscope_auth", obfuscation.MaskString(cfg.PyroscopeAuth, 4, 2)).
		Str("app_name", cfg.AppName).
		Bool("tag_entrypoint", cfg.TagEntrypoint).
		Bool("keep_entrypoint_name", cfg.KeepEntrypointName).
		Str("restart", cfg.Restart).
		Int("rate_bytes", cfg.rateLimit()).
		Int("rate_burst", cfg.rateBurst()).
		Str("version", version.Get()).
		Strs("tags", cfg.AppTags).
		Msg("gospy started")

	profilerCtx, profilerCancel := context.WithCancel(ctx)
	defer profilerCancel()

	drainCtx, drainCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer drainCancel()

	stopSignals := startSignalForwarder(profilerCtx, profilerCancel)
	defer stopSignals()

	stacks := make(chan *collector.Sample, 1000)
	inputClosed := make(chan struct{})
	samples := startSampleCollector(drainCtx, stacks)

	limiter := rate.NewLimiter(rate.Limit(cfg.rateLimit()), cfg.rateBurst())
	metadata := pyroscope.NewAppMetadata(cfg.AppName, staticTags, profilerImpl.GetHZ())
	ingest := startIngestPipeline(drainCtx, cfg, client, metadata, limiter, samples.traces, inputClosed)

	runErr := supervisor.ManageProfiler(profilerCtx, profilerImpl, parserImpl, stacks, cfg.Restart)

	close(stacks)
	<-samples.collectorDone
	close(inputClosed)
	<-ingest.publisherDone
	ingest.pool.Wait()

	if ingest.stats != nil {
		close(ingest.stats)
		ingest.aggregator.Wait()
		<-ingest.statsLogged
	}
	drainCancel()

	log.Info().Msg("shutting down")
	return runErr
}

func startSignalForwarder(ctx context.Context, cancel context.CancelFunc) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		select {
		case sig := <-signals:
			log.Info().Str("signal", sig.String()).Msg("signal received")
			cancel()
		case <-ctx.Done():
		}
	}()

	return func() {
		signal.Stop(signals)
	}
}

func startSampleCollector(ctx context.Context, stacks <-chan *collector.Sample) sampleCollection {
	traces := collector.NewTraceCollector()
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		traces.Collect(ctx, stacks)
	}()

	return sampleCollection{
		traces:        traces,
		collectorDone: collectorDone,
	}
}

func startIngestPipeline(
	ctx context.Context,
	cfg Config,
	client *pyroscope.Client,
	metadata *pyroscope.AppMetadata,
	limiter *rate.Limiter,
	traces *collector.TraceCollector,
	inputClosed <-chan struct{},
) ingestPipeline {
	batches := make(chan *collector.TagCollection, cfg.PyroscopeWorkers)
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		defer close(batches)
		publishTagCollections(ctx, traces, inputClosed, batches)
	}()

	var (
		stats       chan *pyroscope.SendResult
		aggregator  *pyroscope.StatsAggregator
		statsLogged chan struct{}
	)
	if cfg.StatsInterval > 0 && zerolog.GlobalLevel() <= zerolog.InfoLevel {
		stats = make(chan *pyroscope.SendResult, 1000)
		aggregator = pyroscope.NewStatsAggregator(stats, cfg.StatsInterval)
		aggregator.Start(ctx)
		statsLogged = make(chan struct{})
		go func() {
			defer close(statsLogged)
			pyroscope.LogStatsReports(ctx, aggregator.Reports())
		}()
	}

	pool := pyroscope.NewIngestPool(client, metadata, limiter, cfg.PyroscopeWorkers, stats)
	pool.Start(ctx, batches)

	return ingestPipeline{
		stats:         stats,
		aggregator:    aggregator,
		statsLogged:   statsLogged,
		pool:          pool,
		publisherDone: publisherDone,
	}
}

func publishTagCollections(
	ctx context.Context,
	traces *collector.TraceCollector,
	inputClosed <-chan struct{},
	batches chan<- *collector.TagCollection,
) {
	for {
		if batch, ok := traces.ConsumeTag(); ok {
			select {
			case batches <- batch:
			case <-ctx.Done():
				return
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-inputClosed:
			if traces.Len() == 0 {
				return
			}
		case <-traces.Notify():
		}
	}
}

func (cfg Config) rateLimit() int {
	return int(cfg.RateMB * megabyte)
}

func (cfg Config) rateBurst() int {
	return int(cfg.RateBurstMB * megabyte)
}
