// Package cli is the gospy command line: it names the configuration file and the verbosity,
// nothing else lives on it.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	ucli "github.com/urfave/cli/v2"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/version"
)

// DefaultConfigPath is read when neither --config nor GOSPY_CONFIG names a file.
const DefaultConfigPath = "/etc/gospy/gospy.yaml"

// Runner takes the loaded configuration and owns the runtime.
type Runner func(ctx context.Context, cfg app.Config) error

// New builds the gospy command line.
func New(run Runner) *ucli.App {
	var verbosity int

	// Set before parsing so a bad command line is logged with the same timestamps as the rest.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	ucli.VersionFlag = &ucli.BoolFlag{
		Name:    "version",
		Usage:   "print only the version",
		Aliases: []string{"V"},
	}

	return &ucli.App{
		Name:    "gospy",
		Usage:   "Discovers PHP processes, profiles them with phpspy and ships the stacks to Pyroscope",
		Version: version.Get(),
		Authors: []*ucli.Author{
			{
				Name:  "Anton Kolesov",
				Email: "headcrabogon@gmail.com",
			},
		},
		HideHelpCommand:        true,
		UseShortOptionHandling: true,
		Flags: []ucli.Flag{
			&ucli.StringFlag{
				Name:    "config",
				Usage:   "Path of the configuration file",
				EnvVars: []string{"GOSPY_CONFIG"},
				Value:   DefaultConfigPath,
			},
			&ucli.BoolFlag{
				Name:    "verbose",
				Usage:   "Verbosity level; use twice to increase verbosity",
				Aliases: []string{"v"},
				Count:   &verbosity,
			},
		},
		Action: func(c *ucli.Context) error {
			if c.Args().Present() {
				return fmt.Errorf("unexpected argument %q: gospy takes no positional arguments, the configuration lives in the file named by --config", c.Args().First())
			}

			path := c.String("config")
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("cannot read configuration: %w", err)
			}

			cfg, warnings, err := config.Load(content, os.LookupEnv)
			if err != nil {
				return fmt.Errorf("configuration %s: %w", path, err)
			}

			setupLogger(verbosity, cfg.InstanceName)
			log.Info().Str("config", path).Msg("configuration loaded")
			for _, warning := range warnings {
				log.Warn().Msg(warning)
			}

			return run(c.Context, app.Config{Config: cfg})
		},
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
