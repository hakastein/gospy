package main

import (
	"context"
	"errors"
	"fmt"
	"gospy/internal/profiler"
	"gospy/internal/pyroscope"
	"gospy/internal/sample"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
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

func setupLogger(debug bool) (*zap.Logger, error) {
	if debug {
		cfg := zap.NewDevelopmentConfig()
		return cfg.Build()
	}
	cfg := zap.NewProductionConfig()
	return cfg.Build()
}

func recoverAndLogPanic(logger *zap.Logger, message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		if err, ok := r.(error); ok {
			logger.Error(message, zap.Error(err))
		} else {
			logger.Error(message, zap.Any("error", r))
		}
		cancel()
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

	logger, logErr := setupLogger(debug)
	if logErr != nil {
		return logErr
	}
	defer logger.Sync()

	logger.Info("gospy started",
		zap.String("pyroscope_url", pyroscopeURL),
		zap.String("app_name", app),
		zap.String("static_tags", staticTags),
		zap.Duration("accumulation_interval", accumulationInterval),
	)

	samplesChannel := make(chan *sample.Collection)
	signalsChannel := make(chan os.Signal, 1)
	signal.Notify(signalsChannel, syscall.SIGTERM, syscall.SIGINT)

	if len(arguments) == 0 {
		return errors.New("no profiler application specified")
	}

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	profilerName := filepath.Base(profilerApp)

	prof, profilerError := profiler.Run(profilerName, profilerArguments, logger, accumulationInterval, entryPoints, dynamicTags)

	if profilerError != nil {
		return profilerError
	}

	var wg sync.WaitGroup

	// Handle OS signals
	go func() {
		select {
		case sig := <-signalsChannel:
			logger.Info("signal received", zap.String("signal", sig.String()))
			cancel()
		case <-ctx.Done():
		}
	}()

	// Profiler management
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer recoverAndLogPanic(logger, "panic recovered in profiler management goroutine", cancel)

		for {
			select {
			case <-ctx.Done():
				if err := prof.Stop(); err != nil {
					logger.Warn("error stopping profiler", zap.Error(err))
				}
				return
			default:
				logger.Info("starting profiler")
				scanner, err := prof.Start(ctx)
				if err != nil {
					logger.Error("error starting profiler", zap.Error(err))
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
					defer recoverAndLogPanic(logger, "panic recovered in profiler ParseOutput", cancel)
					prof.ParseOutput(ctx, cancel, scanner, samplesChannel)
				}()

				err = prof.Wait()
				if err != nil {
					if ctx.Err() != nil {
						logger.Info("profiler terminated")
					} else {
						logger.Error("profiler exited with error", zap.Error(err))
					}
				} else {
					logger.Info("profiler exited gracefully")
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
		defer recoverAndLogPanic(logger, "panic recovered in sendToPyroscope", cancel)
		pyroscope.SendToPyroscope(ctx, logger, cancel, samplesChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, rateMb)
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	// Ensure profiler process is terminated
	if err := prof.Stop(); err != nil {
		logger.Warn("error stopping profiler", zap.Error(err))
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
		log.Fatal(err)
	}
}
