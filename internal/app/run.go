package app

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/obfuscation"
	"github.com/hakastein/gospy/internal/parser"
	"github.com/hakastein/gospy/internal/profiler"
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

type profilerInstance interface {
	Start(ctx context.Context) (*bufio.Scanner, error)
	Wait() error
	IsConfigurationValid() (bool, error)
	GetHZ() int
}

type parserInstance interface {
	Parse(ctx context.Context, scanner *bufio.Scanner, samplesChannel chan<- *collector.Sample)
}

type dependencies struct {
	newProfiler func(profilerApp string, profilerArguments []string) (profilerInstance, error)
	newParser   func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error)
	newClient   func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client
}

func Run(ctx context.Context, cfg Config) error {
	return runWithDependencies(ctx, cfg, dependencies{
		newProfiler: func(profilerApp string, profilerArguments []string) (profilerInstance, error) {
			return profiler.Init(profilerApp, profilerArguments)
		},
		newParser: func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error) {
			return parser.Init(profilerApp, entryPoints, tagsMapping, tagEntrypoint, keepEntrypointName)
		},
		newClient: func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client {
			httpClient := &http.Client{Timeout: pyroscopeTimeout}
			return pyroscope.NewClient(pyroscopeURL, pyroscopeAuth, httpClient)
		},
	})
}

func runWithDependencies(ctx context.Context, cfg Config, deps dependencies) error {
	staticTags, dynamicTags, err := tag.ParseInput(cfg.AppTags)
	if err != nil {
		return err
	}

	if cfg.ProfilerApp == "" {
		return errors.New("no profiler application specified")
	}

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

	profilerInstance, err := deps.newProfiler(cfg.ProfilerApp, cfg.ProfilerArguments)
	if err != nil {
		return err
	}

	if supported, validationErr := profilerInstance.IsConfigurationValid(); !supported {
		return validationErr
	}

	parserInstance, err := deps.newParser(
		filepath.Base(cfg.ProfilerApp),
		cfg.Entrypoints,
		dynamicTags,
		cfg.TagEntrypoint,
		cfg.KeepEntrypointName,
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	signalsChannel := make(chan os.Signal, 1)
	signal.Notify(signalsChannel, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signalsChannel)

	go func() {
		select {
		case sig := <-signalsChannel:
			log.Info().Str("signal", sig.String()).Msg("signal received")
			cancel()
		case <-ctx.Done():
		}
	}()

	stacksChannel := make(chan *collector.Sample, 1000)
	statsChannel := make(chan *pyroscope.RequestStats, 1000)
	inputClosed := make(chan struct{})

	traceCollector := collector.NewTraceCollector()
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		traceCollector.Collect(ctx, stacksChannel)
	}()

	rateLimiter := rate.NewLimiter(rate.Limit(cfg.rateLimit()), cfg.rateBurst())
	pyroscopeClient := deps.newClient(cfg.PyroscopeURL, cfg.PyroscopeAuth, cfg.PyroscopeTimeout)
	pyroscopeIngester := pyroscope.NewAppMetadata(cfg.AppName, staticTags, profilerInstance.GetHZ())
	statsAggregator := pyroscope.NewStatsAggregator(statsChannel, cfg.StatsInterval)
	statsAggregator.Start(ctx)

	workers := make([]*pyroscope.Worker, 0, cfg.PyroscopeWorkers)
	for workerNumber := 1; workerNumber <= cfg.PyroscopeWorkers; workerNumber++ {
		sender := pyroscope.NewWorker(pyroscopeClient, pyroscopeIngester, traceCollector, rateLimiter, statsChannel)
		sender.Start(ctx, inputClosed)
		workers = append(workers, sender)
	}

	runErr := supervisor.ManageProfiler(ctx, profilerInstance, parserInstance, stacksChannel, cfg.Restart)
	close(stacksChannel)
	<-collectorDone
	close(inputClosed)

	for _, worker := range workers {
		worker.Wait()
	}

	close(statsChannel)
	statsAggregator.Wait()
	cancel()

	log.Info().Msg("shutting down")
	return runErr
}

func (cfg Config) rateLimit() int {
	return int(cfg.RateMB * megabyte)
}

func (cfg Config) rateBurst() int {
	return int(cfg.RateBurstMB * megabyte)
}
