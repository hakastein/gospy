package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

const (
	DefaultRateMB             = 4                // Default ingestion rate limit in MB for Pyroscope
	DefaultAccumulationPeriod = 10 * time.Second // Period for collecting samples
	Megabyte                  = 1048576          // Number of bytes in a megabyte
)

const (
	RestartAlways    = "always"
	RestartOnError   = "onerror"
	RestartOnSuccess = "onsuccess"
	RestartNo        = "no"
)

var validRestartOptions = map[string]bool{
	RestartAlways:    true,
	RestartOnError:   true,
	RestartOnSuccess: true,
	RestartNo:        true,
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
				Usage:   "Verbosity level, use twice to increase verbosity",
				Aliases: []string{"v"},
				Count:   &verbosity,
				Action: func(c *cli.Context, b bool) error {
					if verbosity > 2 {
						return errors.New("verbosity too high")
					}
					return nil
				},
			},
			&cli.StringFlag{
				Name:  "app",
				Usage: "App name for Pyroscope",
			},
			&cli.StringFlag{
				Name:  "restart",
				Usage: "Restart profiler on exit (always, onerror, onsuccess, no). Default: no",
				Value: "no",
				Action: func(c *cli.Context, restart string) error {
					if !validRestartOptions[restart] {
						return fmt.Errorf("invalid restart option: %s", restart)
					}
					return nil
				},
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
				Usage: "Limit traces with certain entrypoint (e.g., index.php)",
			},
			&cli.BoolFlag{
				Name:  "tag-entrypoint",
				Usage: "Add entrypoint to tags",
			},
			&cli.BoolFlag{
				Name:  "keep-entrypoint-name",
				Usage: "Keep entrypoint name in traces. Default: true",
				Value: true,
			},
			&cli.StringFlag{
				Name:  "instance-name",
				Usage: "change name of this instance in logs",
				Value: "gospy",
			},
		},
		Action: func(c *cli.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			instanceName := c.String("instance-name")
			setupLogger(verbosity, instanceName)
			return run(ctx, cancel, c)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err)
	}
}
