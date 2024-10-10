package main

import (
	"context"
	"errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"gospy/internal/parser"
	"gospy/internal/profiler"
	"gospy/internal/pyroscope"
	"gospy/internal/sample"
	"gospy/internal/supervisor"
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
	log.Logger = log.Logger.With().Str("app", instanceName).Logger()
}

func run(ctx context.Context, cancel context.CancelFunc, c *cli.Context) error {
	// setup app
	var (
		pyroscopeURL                     = c.String("pyroscope")
		pyroscopeAuth                    = c.String("pyroscopeAuth")
		accumulationInterval             = c.Duration("accumulation-interval")
		tagEntrypoint                    = c.Bool("tag-entrypoint")
		keepEntrypointName               = c.Bool("keep-entrypoint-name")
		app                              = c.String("app")
		restart                          = c.String("restart")
		rateMb                           = c.Int("rate-mb") * Megabyte
		staticTags, dynamicTags, tagsErr = parseTags(c.StringSlice("tag"))
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
		Str("app_name", app).
		Str("static_tags", staticTags).
		Dur("accumulation_interval", accumulationInterval).
		Msg("gospy started")

	// make channels and ensure closing
	stacksChannel := make(chan [2]string, 1000)
	collectionChannel := make(chan *sample.Collection, 10)
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
	rateHz := profilerInstance.GetHZ()

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

	// collect traces from stacksChannel and compress into foldedStacks collection, populate collectionChannel by period
	go func() {
		defer wg.Done()
		defer close(collectionChannel)

		sample.FoldedStacksToCollection(
			ctx,
			stacksChannel,
			collectionChannel,
			accumulationInterval,
			rateHz,
		)
	}()

	// Send folded stacks from collectionChannel to Pyroscope
	go func() {
		defer wg.Done()
		defer close(signalsChannel)

		pyroscope.SendToPyroscope(
			ctx,
			collectionChannel,
			app,
			staticTags,
			pyroscopeURL,
			pyroscopeAuth,
			rateMb,
		)
	}()

	wg.Wait()
	<-ctx.Done()
	log.Info().Msg("shutting down")

	return nil
}
