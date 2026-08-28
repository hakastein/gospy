package main

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"github.com/hakastein/gospy/internal/app"
)

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

func run(ctx context.Context, c *cli.Context) error {
	arguments := c.Args().Slice()

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
		StatsInterval:      c.Duration("stats-interval"),
	}

	if len(arguments) > 0 {
		cfg.ProfilerApp = arguments[0]
		cfg.ProfilerArguments = arguments[1:]
	}

	return app.Run(ctx, cfg)
}
