package main

import (
	"context"
	"errors"
	"fmt"
	"gospy/internal/profiler"
	"gospy/internal/pyroscope"
	"gospy/internal/sample"
	"os"
	"os/signal"
	"strings"
	"sync"
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

func recoverAndLogPanic(message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		if err, ok := r.(error); ok {
			log.Error().Err(err).Msg(message)
		} else {
			log.Error().Interface("panic", r).Msg(message)
		}
		cancel()
	}
}

func setupLogger(debug bool) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
}

func run(ctx context.Context, cancel context.CancelFunc, c *cli.Context) error {
	pyroscopeURL := c.String("pyroscope")
	pyroscopeAuth := c.String("pyroscopeAuth")
	accumulationInterval := c.Duration("accumulation-interval")
	app := c.String("app")
	debug := c.Bool("debug")
	restart := c.String("restart")
	rateMb := c.Int("rate-mb") * Megabyte
	staticTags, dynamicTags, tagsErr := parseTags(c.StringSlice("tag"))
	entryPoints := mapEntryPoints(c.StringSlice("entrypoint"))
	arguments := c.Args().Slice()

	if tagsErr != nil {
		return tagsErr
	}

	setupLogger(debug)

	log.Info().
		Str("pyroscope_url", pyroscopeURL).
		Str("app_name", app).
		Str("static_tags", staticTags).
		Dur("accumulation_interval", accumulationInterval).
		Msg("gospy started")

	samplesChannel := make(chan *sample.Collection)
	signalsChannel := make(chan os.Signal, 1)
	signal.Notify(signalsChannel, syscall.SIGTERM, syscall.SIGINT)

	if len(arguments) == 0 {
		return errors.New("no profiler application specified")
	}

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	prof, profilerError := profiler.Run(profilerApp, profilerArguments, accumulationInterval, entryPoints, dynamicTags)

	if profilerError != nil {
		return profilerError
	}

	var wg sync.WaitGroup

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverAndLogPanic("panic recovered in profiler management goroutine", cancel)

		for {
			select {
			case <-ctx.Done():
				if err := prof.Stop(); err != nil {
					log.Warn().Err(err).Msg("error stopping profiler")
				}
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
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer recoverAndLogPanic("panic recovered in profiler ParseOutput", cancel)
					prof.ParseOutput(ctx, cancel, scanner, samplesChannel)
				}()

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

	// Send samples to Pyroscope
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverAndLogPanic("panic recovered in sendToPyroscope", cancel)
		pyroscope.SendToPyroscope(ctx, cancel, samplesChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, rateMb)
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	// Ensure profiler process is terminated
	if err := prof.Stop(); err != nil {
		log.Warn().Err(err).Msg("error stopping profiler")
	}

	// Close channels and wait for goroutines
	close(samplesChannel)
	wg.Wait()

	return nil
}

func main() {
	app := &cli.App{
		Name:  "gospy",
		Usage: "A Go wrapper for sampling profilers that sends traces to Pyroscope",
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
				Name:  "debug",
				Usage: "Enable debug logging",
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
			defer cancel()
			return run(ctx, cancel, c)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err)
	}
}
