package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/hakastein/gospy/internal/version"
)

const (
	DefaultRateMB        = 4
	PyroscopeWorkers     = 5
	PyroscopeTimeout     = 10 * time.Second
	DefaultStatsInterval = 10 * time.Second
)

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
				Usage: "Amount of workers who sends data to pyroscope; must be at least 1",
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
				Action: func(_ *cli.Context, restart string) error {
					return supervisor.ValidateRestart(restart)
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
				Usage: "Interval at which the application will log its sending statistics; set to 0 or less to disable statistics logging",
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
			instanceName := c.String("instance-name")
			setupLogger(verbosity, instanceName)
			return run(context.Background(), c)
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("can't start app")
	}
}
