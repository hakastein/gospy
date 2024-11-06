package main

import (
	"context"
	"errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"gospy/internal/collector"
	"gospy/internal/parser"
	"gospy/internal/profiler"
	"gospy/internal/sample"
	"gospy/internal/supervisor"
	"gospy/internal/types"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	// setup app
	var (
		pyroscopeURL                     = c.String("pyroscope")
		pyroscopeAuth                    = c.String("pyroscope-auth")
		accumulationInterval             = c.Duration("accumulation-interval")
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
		Dur("accumulation_interval", accumulationInterval).
		Msg("gospy started")

	stacksChannel := make(chan *types.Sample, 1000)
	collectionChannel := make(chan *sample.Collection, 100)
	signalsChannel := make(chan os.Signal, 1)

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	profilerInstance, profilerError := profiler.Init(profilerApp, profilerArguments)
	if profilerError != nil {
		return profilerError
	}
	// terminate app if profiler arguments isn't supported by gospy
	if sup, unsupportableError := profilerInstance.IsConfigurationValid(); !sup {
		return unsupportableError
	}
	// get sample rate from profiler settings
	sampleingRateHZ := profilerInstance.GetHZ()

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
	wg.Add(3)

	go func() {
		defer wg.Done()
		defer close(stacksChannel)

		// run profiles and parser, transform traces to stack format and send to stacksChannel
		supervisor.ManageProfiler(
			ctx,
			profilerInstance,
			parserInstance,
			stacksChannel,
			restart,
		)
	}()

	collector := collector.NewCollector()
	collector.ReadFrom(ctx, stacksChannel)

	wg.Wait()
	<-ctx.Done()
	log.Info().Msg("shutting down")

	return nil
}
