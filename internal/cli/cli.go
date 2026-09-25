package cli

import (
	"context"
	"strings"
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

	// Set before parsing so a missing required flag is logged with the same timestamps as the rest.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	ucli.VersionFlag = &ucli.BoolFlag{
		Name:    "version",
		Usage:   "print only the version",
		Aliases: []string{"V"},
	}

	flags := []ucli.Flag{
		&ucli.StringFlag{
			Name:     "pyroscope",
			Usage:    "Pyroscope server URL",
			Required: true,
		},
		&ucli.StringFlag{
			Name:    "pyroscope-auth",
			Usage:   "Authentication token for Pyroscope; prefer the GOSPY_PYROSCOPE_AUTH environment variable, because the command line is readable by every process in the PID namespace",
			EnvVars: []string{"GOSPY_PYROSCOPE_AUTH"},
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
			Name:     "app",
			Usage:    "App name for Pyroscope",
			Required: true,
		},
		&ucli.StringSliceFlag{
			Name:  "tag",
			Usage: `Static and dynamic tags: key=value, or key={{"meta.key"}} / key={{"meta.key" "regex" "replacement"}}`,
		},
		&ucli.BoolFlag{
			Name:  "tag-entrypoint",
			Usage: "Add entry point to tags",
		},
		&ucli.Float64Flag{
			Name:  "rate-mb",
			Usage: "Ingestion rate limit in MB; 0 means unlimited",
			Value: DefaultRateMB,
		},
		&ucli.Float64Flag{
			Name:  "rate-burst-mb",
			Usage: "Ingestion rate limit burst in MB; must be above zero unless the rate limit is unlimited",
			Value: DefaultRateMB + DefaultRateMB/2,
		},
		&ucli.StringFlag{
			Name:  "restart",
			Usage: "Restart profiler on exit (always, onerror, onsuccess, no). Default: no",
			Value: supervisor.RestartNo,
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
		&ucli.DurationFlag{
			Name:  "drain-timeout",
			Usage: "How long a shutdown keeps sending buffered batches before dropping them; a second SIGTERM or SIGINT ends it at once",
			Value: app.DefaultDrainTimeout,
		},
		&ucli.BoolFlag{
			Name:    "verbose",
			Usage:   "Verbosity level; use twice to increase verbosity",
			Aliases: []string{"v"},
			Count:   &verbosity,
		},
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
		Flags:                     flags,
		Action: func(c *ucli.Context) error {
			setupLogger(verbosity, c.String("instance-name"))

			cfg := configFrom(c)
			warnMisplacedFlags(flags, cfg.ProfilerArguments)

			return run(c.Context, cfg)
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
		DrainTimeout:       c.Duration("drain-timeout"),
	}

	if arguments := c.Args().Slice(); len(arguments) > 0 {
		cfg.ProfilerApp = arguments[0]
		cfg.ProfilerArguments = arguments[1:]
	}

	return cfg
}

// flags excludes the help and version flags urfave adds: phpspy has --help and --version of its own.
// Arguments after "--" belong to the command phpspy traces.
func warnMisplacedFlags(flags []ucli.Flag, profilerArguments []string) {
	names := make(map[string]struct{})
	for _, flag := range flags {
		for _, name := range flag.Names() {
			names[name] = struct{}{}
		}
	}

	for _, argument := range profilerArguments {
		if argument == "--" {
			return
		}

		if !strings.HasPrefix(argument, "-") {
			continue
		}

		name, _, _ := strings.Cut(strings.TrimLeft(argument, "-"), "=")
		if _, known := names[name]; known {
			log.Warn().
				Str("argument", argument).
				Msg("a gospy flag after the profiler command is passed to the profiler, not to gospy")
		}
	}
}

func setupLogger(verbose int, instanceName string) {
	switch {
	case verbose >= 2:
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case verbose == 1:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	log.Logger = log.Logger.With().Str("instance", instanceName).Logger()
}
