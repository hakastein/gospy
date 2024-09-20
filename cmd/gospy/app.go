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
	"os"
	"os/signal"
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
	defer close(foldedStacksChannel)
	collectionChannel := make(chan *sample.Collection, 10)
	defer close(collectionChannel)
	signalsChannel := make(chan os.Signal, 1)
	defer close(signalsChannel)

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	profilerInstance, profilerError := profiler.Run(profilerApp, profilerArguments)
	if profilerError != nil {
		return profilerError
	}
	// terminate app if profiler arguments isn't supported by gospy
	if sup, unsupportableError := profilerInstance.IsConfigurationValid(); !sup {
		return unsupportableError
	}
	// get sample rate from profiler settings
	rateHz := profilerInstance.GetHZ()

	parserInstance, parserError := parser.Get(profilerApp, entryPoints, dynamicTags)
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

	// run profiler and parser
	go func() {
		defer recoverAndCancel("panic recovered in profiler management goroutine", cancel)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				log.Info().Msg("starting profiler")
				scanner, err := profilerInstance.Start(ctx)

				if err != nil {
					// terminate gospy if got startup error
					log.Error().Err(err).Msg("error starting profiler")
					cancel()
					return
				}

				// Parse profiler output
				parserInstance.Parse(ctx, scanner, foldedStacksChannel)

				err = profilerInstance.Wait()
				if err != nil {
					if ctx.Err() != nil {
						log.Info().Msg("profiler terminated")
					} else {
						log.Error().Err(err).Msg("profiler exited with error")
					}
				} else {
					log.Info().Msg("profiler exited gracefully")
				}

				if ctx.Err() != nil {
					return
				}

				switch {
				case restart == "always":
					continue
				case restart == "onerror" && err != nil:
					continue
				case restart == "onsuccess" && err == nil:
					continue
				default:
					cancel()
					return
				}
			}
		}
	}()

	// collect folded stacks and compact into collection
	go func() {
		sample.FoldedStacksToCollection(foldedStacksChannel, collectionChannel, accumulationInterval, rateHz)
	}()

	// Send samples to Pyroscope
	go func() {
		defer recoverAndCancel("panic recovered in sendToPyroscope", cancel)
		pyroscope.SendToPyroscope(ctx, collectionChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, rateMb)
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	// Ensure profiler process is terminated
	if err := profilerInstance.Stop(); err != nil {
		log.Warn().Err(err).Msg("error stopping profiler")
	} else {
		log.Info().Msg("profiler process terminated")
	}

	return nil
}
