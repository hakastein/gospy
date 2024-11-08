package main

import (
	"context"
	"errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"golang.org/x/time/rate"
	"gospy/internal/collector"
	"gospy/internal/parser"
	"gospy/internal/profiler"
	"gospy/internal/pyroscope"
	"gospy/internal/supervisor"
	"gospy/internal/types"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func setupLogger(verbose int, instanceName string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if verbose == 1 {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	if verbose > 1 {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	}
	log.Logger = log.Logger.With().Str("instance", instanceName).Logger()
}

func run(ctx context.Context, cancel context.CancelFunc, c *cli.Context) error {
	// Setup app
	var (
		pyroscopeURL                     = c.String("pyroscope")
		pyroscopeAuth                    = c.String("pyroscope-auth")
		pyroscopeWorkers                 = c.Int("pyroscope-workers")
		pyroscopeTimeout                 = c.Duration("pyroscope-timeout")
		tagEntrypoint                    = c.Bool("tag-entrypoint")
		keepEntrypointName               = c.Bool("keep-entrypoint-name")
		app                              = c.String("app")
		restart                          = c.String("restart")
		rateLimit                        = int(c.Float64("rate-mb") * Megabyte)
		appTags                          = c.StringSlice("tag")
		staticTags, dynamicTags, tagsErr = parseTags(appTags)
		entryPoints                      = c.StringSlice("entrypoint")
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
		Str("pyroscope_auth", maskString(pyroscopeAuth, 4, 2)).
		Str("app_name", app).
		Bool("tag_entrypoint", tagEntrypoint).
		Bool("keep_entrypoint_name", keepEntrypointName).
		Str("restart", restart).
		Int("rate_bytes", rateLimit).
		Str("version", Version).
		Strs("tags", appTags).
		Msg("gospy started")

	stacksChannel := make(chan *types.Sample, 1000)
	signalsChannel := make(chan os.Signal, 1)
	statsChannel := make(chan pyroscope.RequestStats, 1000)

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
		supervisor.ManageProfiler(
			ctx,
			profilerInstance,
			parserInstance,
			stacksChannel,
			restart,
		)
	}()

	rateLimiter := rate.NewLimiter(rate.Limit(rateLimit), rateLimit*2)

	traceCollector := collector.NewTraceCollector()
	traceCollector.Subscribe(ctx, stacksChannel)

	pyroscopeClient := pyroscope.NewClient(
		ctx,
		pyroscopeURL,
		pyroscopeAuth,
		app,
		staticTags,
		samplingRateHZ,
		pyroscopeTimeout,
	)

	pyroscope.StartStatsAggregator(ctx, statsChannel, 10*time.Second)

	for i := 0; i < pyroscopeWorkers; i++ {
		sender := pyroscope.NewWorker(pyroscopeClient, traceCollector, rateLimiter, statsChannel)
		sender.Start(ctx)
	}

	wg.Wait()
	<-ctx.Done()
	log.Info().Msg("shutting down")

	return nil
}
