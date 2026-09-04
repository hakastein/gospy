package cli

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	ucli "github.com/urfave/cli/v2"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/hakastein/gospy/internal/version"
)

const (
	DefaultRateMB        = 4
	PyroscopeWorkers     = 5
	PyroscopeTimeout     = 10 * time.Second
	DefaultStatsInterval = 10 * time.Second
)

// Runner takes the configuration the command line describes and owns the pipeline.
type Runner func(ctx context.Context, cfg app.Config) error

// New builds the gospy command line: flags belong to gospy up to the first non-flag word,
// which starts the profiler command and takes the remaining arguments with it.
func New(run Runner) *ucli.App {
	var verbosity int

	ucli.VersionFlag = &ucli.BoolFlag{
		Name:    "version",
		Usage:   "print only the version",
		Aliases: []string{"V"},
	}

	return &ucli.App{
		Name:    "gospy",
		Usage:   "A Go wrapper for sampling profilers that sends traces to Pyroscope",
		Version: version.Get(),
		Authors: []*ucli.Author{
			{
				Name:  "Anton Kolesov",
				Email: "headcrabogon@gmail.com",
			},
		},
		UseShortOptionHandling:    true,
		DisableSliceFlagSeparator: true,
		Flags: []ucli.Flag{
			&ucli.StringFlag{
				Name:     "pyroscope",
				Usage:    "Pyroscope server URL",
				Required: true,
			},
			&ucli.StringFlag{
				Name:  "pyroscope-auth",
				Usage: "Authentication token for Pyroscope",
			},
			&ucli.DurationFlag{
				Name:  "pyroscope-timeout",
				Usage: "Timeout to pyroscope request",
				Value: PyroscopeTimeout,
			},
			&ucli.IntFlag{
				Name:  "pyroscope-workers",
				Usage: "Amount of workers who sends data to pyroscope; must be at least 1",
				Value: PyroscopeWorkers,
			},
			&ucli.StringFlag{
				Name:  "app",
				Usage: "App name for Pyroscope",
			},
			&ucli.StringSliceFlag{
				Name:  "tag",
				Usage: "Static and dynamic tags (key=value or key=%value%)",
			},
			&ucli.BoolFlag{
				Name:  "tag-entrypoint",
				Usage: "Add entry point to tags",
			},
			&ucli.Float64Flag{
				Name:  "rate-mb",
				Usage: "Ingestion rate limit in MB",
				Value: DefaultRateMB,
			},
			&ucli.Float64Flag{
				Name:  "rate-burst-mb",
				Usage: "Ingestion rate limit burst in MB",
				Value: DefaultRateMB + DefaultRateMB/2,
			},
			&ucli.StringFlag{
				Name:  "restart",
				Usage: "Restart profiler on exit (always, onerror, onsuccess, no). Default: no",
				Value: supervisor.RestartNo,
				Action: func(_ *ucli.Context, restart string) error {
					return supervisor.ValidateRestart(restart)
				},
			},
			&ucli.StringSliceFlag{
				Name:  "entrypoint",
				Usage: "Limit traces to certain entry points (e.g., index.php)",
			},
			&ucli.BoolFlag{
				Name:  "keep-entrypoint-name",
				Usage: "Keep entry point name in traces. Default: true",
				Value: true,
			},
			&ucli.StringFlag{
				Name:  "instance-name",
				Usage: "Change the name of this gospy instance (for logging purposes only)",
				Value: "gospy",
			},
			&ucli.DurationFlag{
				Name:  "batch-interval",
				Usage: "Window over which samples are accumulated before a batch is sent to Pyroscope",
				Value: app.DefaultBatchInterval,
			},
			&ucli.DurationFlag{
				Name:  "stats-interval",
				Usage: "Interval at which the application will log its sending statistics; set to 0 or less to disable statistics logging",
				Value: DefaultStatsInterval,
			},
			&ucli.BoolFlag{
				Name:    "verbose",
				Usage:   "Verbosity level; use twice to increase verbosity",
				Aliases: []string{"v"},
				Count:   &verbosity,
			},
		},
		Action: func(c *ucli.Context) error {
			setupLogger(verbosity, c.String("instance-name"))

			return run(c.Context, configFrom(c))
		},
	}
}

func configFrom(c *ucli.Context) app.Config {
	cfg := app.Config{
		PyroscopeURL:       c.String("pyroscope"),
		PyroscopeAuth:      c.String("pyroscope-auth"),
		PyroscopeWorkers:   c.Int("pyroscope-workers"),
		PyroscopeTimeout:   c.Duration("pyroscope-timeout"),
		TagEntrypoint:      c.Bool("tag-entrypoint"),
		KeepEntrypointName: c.Bool("keep-entrypoint-name"),
		AppName:            c.String("app"),
		Restart:            c.String("restart"),
		RateMB:             c.Float64("rate-mb"),
		RateBurstMB:        c.Float64("rate-burst-mb"),
		AppTags:            c.StringSlice("tag"),
		Entrypoints:        c.StringSlice("entrypoint"),
		BatchInterval:      c.Duration("batch-interval"),
		StatsInterval:      c.Duration("stats-interval"),
	}

	if arguments := c.Args().Slice(); len(arguments) > 0 {
		cfg.ProfilerApp = arguments[0]
		cfg.ProfilerArguments = arguments[1:]
	}

	return cfg
}

func setupLogger(verbose int, instanceName string) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	switch {
	case verbose == 1:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case verbose == 2:
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	log.Logger = log.Logger.With().Str("instance", instanceName).Logger()
}
