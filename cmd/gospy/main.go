package main

import (
	"context"
	"errors"
	"fmt"
	"gospy/internal/parser"
	"gospy/internal/profiler"
	"gospy/internal/pyroscope"
	"gospy/internal/sample"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

const (
	DefaultRateMB             = 4                // Default ingestion rate limit in MB for Pyroscope
	DefaultAccumulationPeriod = 10 * time.Second // Period for collecting samples
	Megabyte                  = 1048576          // Number of bytes in a megabyte
)

// parseTags processes input tags and separates static and dynamic tags
func parseTags(tagsInput []string) (string, map[string]string, error) {
	dynamicTags := make(map[string]string)
	var staticTags []string

	for _, tag := range tagsInput {
		idx := strings.Index(tag, "=")
		if idx != -1 {
			key := tag[:idx]
			value := tag[idx+1:]
			if len(value) > 1 && value[0] == '%' && value[len(value)-1] == '%' {
				dynamicTags[value[1:len(value)-1]] = key
			} else {
				staticTags = append(staticTags, tag)
			}
		} else {
			return "", nil, fmt.Errorf("unexpected tag format %s", tag)
		}
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}

func mapEntryPoints(entryPoints []string) map[string]struct{} {
	entryMap := make(map[string]struct{}, len(entryPoints))
	for _, entry := range entryPoints {
		entryMap[entry] = struct{}{}
	}
	return entryMap
}

func recoverAndCancel(message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		var err error
		switch x := r.(type) {
		case string:
			err = errors.New(x)
		case error:
			err = x
		default:
			err = errors.New("unknown panic")
		}
		log.Err(err).
			Stack().
			Str("stack", string(debug.Stack())).
			Msg(message)
		cancel()
	}
}

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

	foldedStacksChannel := make(chan [2]string, 1000)
	collectionChannel := make(chan *sample.Collection, 10)
	signalsChannel := make(chan os.Signal, 1)

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	prof, profilerError := profiler.Run(profilerApp, profilerArguments)
	if profilerError != nil {
		return profilerError
	}
	if sup, unsupportableError := prof.IsSupportable(); !sup {
		return unsupportableError
	}
	rateHz := prof.GetHZ()

	pars, parserError := parser.Get(profilerApp, entryPoints, dynamicTags)
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

	// Profiler management
	go func() {
		defer recoverAndCancel("panic recovered in profiler management goroutine", cancel)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				log.Info().Msg("starting profiler")
				scanner, err := prof.Start(ctx)
				if err != nil {
					log.Error().Err(err).Msg("error starting profiler")
					if restart == "always" || (restart == "onerror" && err != nil) {
						continue
					} else {
						cancel()
						return
					}
				}

				// Parse profiler output
				pars.Parse(ctx, scanner, foldedStacksChannel)

				err = prof.Wait()
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

	// Collect and compress tracess
	ticker := time.NewTicker(accumulationInterval)
	defer ticker.Stop()
	collection := sample.NewCollection(rateHz)

	// collect folded stacks and compact into collection
	go func() {
		defer recoverAndCancel("panic recovered in sendToPyroscope", cancel)

		for {
			select {
			case <-ticker.C:
				if collection.Len() == 0 {
					continue
				}
				collection.Finish()
				collectionChannel <- collection
				collection = sample.NewCollection(rateHz)
			case stack, ok := <-foldedStacksChannel:
				if !ok {
					return
				}
				collection.AddSample(stack[0], stack[1])
			}
		}
	}()

	// Send samples to Pyroscope
	go func() {
		defer recoverAndCancel("panic recovered in sendToPyroscope", cancel)
		pyroscope.SendToPyroscope(ctx, collectionChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, rateMb)
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	// Ensure profiler process is terminated
	if err := prof.Stop(); err != nil {
		log.Warn().Err(err).Msg("error stopping profiler")
	} else {
		log.Info().Msg("profiler process terminated")
	}

	// Close channels and wait for goroutines
	close(foldedStacksChannel)

	return nil
}

func main() {
	var verbosity int
	app := &cli.App{
		Name:                   "gospy",
		Usage:                  "A Go wrapper for sampling profilers that sends traces to Pyroscope",
		UseShortOptionHandling: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "pyroscope",
				Usage:    "Pyroscope server URL",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "pyroscopeAuth",
				Usage: "Pyroscope authentication token",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Verbosity level, use multiply time to increase verbosity",
				Aliases: []string{"v"},
				Count:   &verbosity,
			},
			&cli.StringFlag{
				Name:  "app",
				Usage: "App name for Pyroscope",
			},
			&cli.StringFlag{
				Name:  "restart",
				Usage: "Restart profiler on exit (always, onerror, onsuccess, no). Default: no",
				Value: "no",
			},
			&cli.StringSliceFlag{
				Name:  "tag",
				Usage: "Static and dynamic tags (key=value or key=%value%)",
			},
			&cli.DurationFlag{
				Name:  "accumulation-interval",
				Usage: "Interval between sending accumulated samples to Pyroscope",
				Value: DefaultAccumulationPeriod,
			},
			&cli.IntFlag{
				Name:  "rate-mb",
				Usage: "Ingestion rate limit in MB",
				Value: DefaultRateMB,
			},
			&cli.StringSliceFlag{
				Name:  "entrypoint",
				Usage: "Entrypoint filenames to collect data from (e.g., index.php)",
			},
		},
		Action: func(c *cli.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			setupLogger(verbosity)
			defer cancel()
			return run(ctx, cancel, c)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err)
	}
}
