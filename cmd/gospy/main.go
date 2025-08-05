package main

import (
	"context"
	"fmt"
	"github.com/hakastein/gospy/internal/version"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"os"
	"time"
)

const (
	DefaultRateMB        = 4       // Default ingestion rate limit in MB for Pyroscope
	Megabyte             = 1048576 // Number of bytes in a megabyte
	PyroscopeWorkers     = 5       // Amount of pyroscope senders
	PyroscopeTimeout     = 10 * time.Second
	DefaultStatsInterval = 10 * time.Second
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
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Usage:   "print only the version",
		Aliases: []string{"V"},
	}
	app := &cli.App{
		Name:    "gospy",
		Usage:   "A Go wrapper for sampling profilers that sends traces to Pyroscope",
		Version: version.Get(),
		Authors: []*cli.Author{
			{
				Name:  "Anton Kolesov",
				Email: "headcrabogon@gmail.com",
			},
		},
		UseShortOptionHandling:    true,
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "pyroscope",
				Usage:    "Pyroscope server URL",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "pyroscope-auth",
				Usage: "Authentication token for Pyroscope",
			},
			&cli.DurationFlag{
				Name:  "pyroscope-timeout",
				Usage: "Timeout to pyroscope request",
				Value: PyroscopeTimeout,
			},
			&cli.IntFlag{
				Name:  "pyroscope-workers",
				Usage: "Amount of workers who sends data to pyroscope",
				Value: PyroscopeWorkers,
			},
			&cli.StringFlag{
				Name:  "app",
				Usage: "App name for Pyroscope",
			},
			&cli.StringSliceFlag{
				Name:  "tag",
				Usage: "Static and dynamic tags (key=value or key=%value%)",
			},
			&cli.BoolFlag{
				Name:  "tag-entrypoint",
				Usage: "Add entry point to tags",
			},
			&cli.Float64Flag{
				Name:  "rate-mb",
				Usage: "Ingestion rate limit in MB",
				Value: DefaultRateMB,
			},
			&cli.Float64Flag{
				Name:  "rate-burst-mb",
				Usage: "Ingestion rate limit burst in MB",
				Value: DefaultRateMB + DefaultRateMB/2,
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
				Name:  "entrypoint",
				Usage: "Limit traces to certain entry points (e.g., index.php)",
			},
			&cli.BoolFlag{
				Name:  "keep-entrypoint-name",
				Usage: "Keep entry point name in traces. Default: true",
				Value: true,
			},
			&cli.StringFlag{
				Name:  "instance-name",
				Usage: "Change the name of this gospy instance (for logging purposes only)",
				Value: "gospy",
			},
			&cli.DurationFlag{
				Name:  "stats-interval",
				Usage: "Interval at which the application will log its sending statistics",
				Value: DefaultStatsInterval,
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Verbosity level; use twice to increase verbosity",
				Aliases: []string{"v"},
				Count:   &verbosity,
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
		log.Fatal().Err(err).Msg("can't start app")
	}
}
