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

func setupLogger(verbose int) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if verbose == 1 {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	if verbose > 1 {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	}
}

func run(ctx context.Context, cancel context.CancelFunc, c *cli.Context) error {
	// setup app
	pyroscopeURL := c.String("pyroscope")
	pyroscopeAuth := c.String("pyroscopeAuth")
	accumulationInterval := c.Duration("accumulation-interval")
	app := c.String("app")
	restart := c.String("restart")
	rateMb := c.Int("rate-mb") * Megabyte
	staticTags, dynamicTags, tagsErr := parseTags(c.StringSlice("tag"))
	entryPoints := mapEntryPoints(c.StringSlice("entrypoint"))
	arguments := c.Args().Slice()

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
	foldedStacksChannel := make(chan [2]string, 1000)
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

	parserInstance, parserError := parser.Init(profilerApp, entryPoints, dynamicTags)
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

	// run profiler and parser
	go func() {
		defer wg.Done()
		defer close(foldedStacksChannel)

		supervisor.ManageProfiler(ctx, profilerInstance, parserInstance, foldedStacksChannel, restart)
	}()

	// collect folded stacks and compact into collection
	go func() {
		defer wg.Done()
		defer close(collectionChannel)

		sample.FoldedStacksToCollection(ctx, foldedStacksChannel, collectionChannel, accumulationInterval, rateHz)
	}()

	// Send samples to Pyroscope
	go func() {
		defer wg.Done()
		defer close(signalsChannel)

		pyroscope.SendToPyroscope(ctx, collectionChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, rateMb)
	}()

	wg.Wait()
	<-ctx.Done()
	log.Info().Msg("shutting down")

	return nil
}
