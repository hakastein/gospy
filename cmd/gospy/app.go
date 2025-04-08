package main

import (
	"context"
	"errors"
	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/obfuscation"
	"github.com/hakastein/gospy/internal/parser"
	"github.com/hakastein/gospy/internal/profiler"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"golang.org/x/time/rate"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func setupLogger(verbose int, instanceName string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	switch {
	case verbose == 1:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case verbose == 2:
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	log.Logger = log.Logger.With().Str("instance", instanceName).Logger()
}

func run(ctx context.Context, cancel context.CancelFunc, c *cli.Context) error {
	var (
		pyroscopeURL                     = c.String("pyroscope")
		pyroscopeAuth                    = c.String("pyroscope-auth")
		pyroscopeWorkers                 = c.Int("pyroscope-workers")
		pyroscopeTimeout                 = c.Duration("pyroscope-timeout")
		tagEntrypoint                    = c.Bool("tag-entrypoint")
		keepEntrypointName               = c.Bool("keep-entrypoint-name")
		appName                          = c.String("app")
		restart                          = c.String("restart")
		rateLimit                        = int(c.Float64("rate-mb") * Megabyte)
		rateBurst                        = int(c.Float64("rate-burst-mb") * Megabyte)
		appTags                          = c.StringSlice("tag")
		staticTags, dynamicTags, tagsErr = tag.ParseInput(appTags)
		entryPoints                      = c.StringSlice("entrypoint")
		statsInterval                    = c.Duration("stats-interval")
		arguments                        = c.Args().Slice()
	)

	if tagsErr != nil {
		return tagsErr
	}

	if len(arguments) == 0 {
		return errors.New("no profiler application specified")
	}

	log.Info().
		Str("pyroscope_url", pyroscopeURL).
		Str("pyroscope_auth", obfuscation.MaskString(pyroscopeAuth, 4, 2)).
		Str("app_name", appName).
		Bool("tag_entrypoint", tagEntrypoint).
		Bool("keep_entrypoint_name", keepEntrypointName).
		Str("restart", restart).
		Int("rate_bytes", rateLimit).
		Int("rate_burst", rateBurst).
		Str("version", Version).
		Strs("tags", appTags).
		Msg("gospy started")

	stacksChannel := make(chan *collector.Sample, 1000)
	signalsChannel := make(chan os.Signal, 1)
	statsChannel := make(chan *pyroscope.RequestStats, 1000)

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	profilerInstance, profilerError := profiler.Init(profilerApp, profilerArguments)
	if profilerError != nil {
		return profilerError
	}
	// Terminate app if profiler arguments aren't supported by gospy
	if sup, unsupportableError := profilerInstance.IsConfigurationValid(); !sup {
		return unsupportableError
	}
	// Get sample rate from profiler settings
	samplingRateHZ := profilerInstance.GetHZ()

	parserInstance, parserError := parser.Init(
		profilerApp,
		entryPoints,
		dynamicTags,
		tagEntrypoint,
		keepEntrypointName,
	)
	if parserError != nil {
		return parserError
	}

	signal.Notify(signalsChannel, syscall.SIGTERM, syscall.SIGINT)
	// Handle OS signals
	go func() {
		select {
		case sig := <-signalsChannel:
			log.Info().Str("signal", sig.String()).Msg("signal received")
			cancel()
		case <-ctx.Done():
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer close(stacksChannel)
		defer wg.Done()

		// Run profiles and parser, transform traces to stack format and send to stacksChannel
		// Restart profiler if set
		supervisor.ManageProfiler(
			ctx,
			profilerInstance,
			parserInstance,
			stacksChannel,
			restart,
		)
	}()

	rateLimiter := rate.NewLimiter(rate.Limit(rateLimit), rateBurst)

	// Trace collector is queue-like struct
	traceCollector := collector.NewTraceCollector()
	traceCollector.Subscribe(ctx, stacksChannel)

	httpClient := &http.Client{
		Timeout: pyroscopeTimeout,
	}

	pyroscopeClient := pyroscope.NewClient(
		pyroscopeURL,
		pyroscopeAuth,
		httpClient,
	)

	pyroscopeIngester := pyroscope.NewAppMetadata(appName, staticTags, samplingRateHZ)

	pyroscope.StartStatsAggregator(ctx, statsChannel, statsInterval)

	for workerNumber := 1; workerNumber <= pyroscopeWorkers; workerNumber++ {
		// each worker will consume traces by tag from the traceCollector queue
		sender := pyroscope.NewWorker(pyroscopeClient, pyroscopeIngester, traceCollector, rateLimiter, statsChannel)
		sender.Start(ctx)
	}

	wg.Wait()
	<-ctx.Done()
	log.Info().Msg("shutting down")

	return nil
}
