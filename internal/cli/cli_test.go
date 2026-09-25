package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/cli"
)

const (
	pyroscopeURL = "http://pyroscope.test"
	appName      = "checkout"
)

func defaultConfig() app.Config {
	return app.Config{
		PyroscopeURL:       pyroscopeURL,
		AppName:            appName,
		PyroscopeWorkers:   5,
		PyroscopeTimeout:   10 * time.Second,
		KeepEntrypointName: true,
		Restart:            "no",
		RateMB:             4,
		RateBurstMB:        6,
		BatchInterval:      5 * time.Second,
		StatsInterval:      10 * time.Second,
		DrainTimeout:       10 * time.Second,
	}
}

func TestNew(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		env     map[string]string
		want    func(*app.Config)
		wantErr string
	}{
		{
			name: "runs with defaults when only the required flags are given",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "phpspy"},
			want: func(cfg *app.Config) {
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "gospy flags end at the first non-flag word",
			args: []string{
				"gospy",
				"--pyroscope", pyroscopeURL,
				"--app", "billing",
				"--tag-entrypoint",
				"phpspy", "-P", "php-fpm", "--rate-hz", "99",
			},
			want: func(cfg *app.Config) {
				cfg.AppName = "billing"
				cfg.TagEntrypoint = true
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{"-P", "php-fpm", "--rate-hz", "99"}
			},
		},
		{
			name: "flags after the profiler command belong to the profiler",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "phpspy", "--app", "not-gospy"},
			want: func(cfg *app.Config) {
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{"--app", "not-gospy"}
			},
		},
		{
			name: "entry point name is kept unless it is turned off",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "--keep-entrypoint-name=false", "phpspy"},
			want: func(cfg *app.Config) {
				cfg.KeepEntrypointName = false
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "repeated tag and entrypoint flags accumulate",
			args: []string{
				"gospy",
				"--pyroscope", pyroscopeURL,
				"--app", appName,
				"--tag", "env=production",
				"--tag", `uri={{ "glopeek server.REQUEST_URI" }}`,
				"--entrypoint", "index.php",
				"--entrypoint", "console",
				"phpspy",
			},
			want: func(cfg *app.Config) {
				cfg.AppTags = []string{"env=production", `uri={{ "glopeek server.REQUEST_URI" }}`}
				cfg.Entrypoints = []string{"index.php", "console"}
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "pyroscope token is read from the environment",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "phpspy"},
			env:  map[string]string{"GOSPY_PYROSCOPE_AUTH": "token-from-env"},
			want: func(cfg *app.Config) {
				cfg.PyroscopeAuth = "token-from-env"
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "pyroscope token on the command line wins over the environment",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "--pyroscope-auth", "token-from-flag", "phpspy"},
			env:  map[string]string{"GOSPY_PYROSCOPE_AUTH": "token-from-env"},
			want: func(cfg *app.Config) {
				cfg.PyroscopeAuth = "token-from-flag"
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "drain timeout is taken from its flag",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "--drain-timeout", "3s", "phpspy"},
			want: func(cfg *app.Config) {
				cfg.DrainTimeout = 3 * time.Second
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name:    "missing pyroscope url is rejected",
			args:    []string{"gospy", "--app", appName, "phpspy"},
			wantErr: `Required flag "pyroscope" not set`,
		},
		{
			name:    "missing app name is rejected",
			args:    []string{"gospy", "--pyroscope", pyroscopeURL, "phpspy"},
			wantErr: `Required flag "app" not set`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}

			var (
				started bool
				got     app.Config
			)

			command := cli.New(func(_ context.Context, cfg app.Config) error {
				started = true
				got = cfg
				return nil
			})
			command.Writer = io.Discard
			command.ErrWriter = io.Discard

			err := command.Run(tc.args)

			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.False(t, started, "pipeline started despite an invalid command line")
				return
			}

			require.NoError(t, err)
			require.True(t, started)

			want := defaultConfig()
			tc.want(&want)
			require.Equal(t, want, got)
		})
	}
}

func TestNewWarnsAboutGospyFlagsAfterTheProfilerCommand(t *testing.T) {
	testCases := []struct {
		name         string
		profilerArgs []string
		wantWarned   []string
	}{
		{
			name:         "long flag",
			profilerArgs: []string{"-P", "php-fpm", "--app", "billing"},
			wantWarned:   []string{"--app"},
		},
		{
			name:         "flag with an inline value",
			profilerArgs: []string{"--restart=always"},
			wantWarned:   []string{"--restart=always"},
		},
		{
			name:         "short alias",
			profilerArgs: []string{"-p", "123", "-v"},
			wantWarned:   []string{"-v"},
		},
		{
			name:         "phpspy's own help and version",
			profilerArgs: []string{"--help", "--version", "-V", "74"},
		},
		{
			name:         "arguments of the traced command",
			profilerArgs: []string{"--", "php", "--app", "-v"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			restoreGlobalLevel(t)
			logs := captureLogs(t)

			command := cli.New(func(context.Context, app.Config) error { return nil })
			command.Writer = io.Discard
			command.ErrWriter = io.Discard

			args := append([]string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName, "phpspy"}, tc.profilerArgs...)
			require.NoError(t, command.Run(args))

			var warned []string
			decoder := json.NewDecoder(logs)
			for decoder.More() {
				var entry struct {
					Level    string `json:"level"`
					Argument string `json:"argument"`
				}
				require.NoError(t, decoder.Decode(&entry))
				if entry.Level == zerolog.WarnLevel.String() && entry.Argument != "" {
					warned = append(warned, entry.Argument)
				}
			}

			require.Equal(t, tc.wantWarned, warned)
		})
	}
}

func TestNewSetsVerbosity(t *testing.T) {
	testCases := []struct {
		name      string
		verbosity []string
		want      zerolog.Level
	}{
		{name: "info by default", want: zerolog.InfoLevel},
		{name: "one flag means debug", verbosity: []string{"-v"}, want: zerolog.DebugLevel},
		{name: "two flags mean trace", verbosity: []string{"-vv"}, want: zerolog.TraceLevel},
		{name: "more than two flags stay at trace", verbosity: []string{"-vvv"}, want: zerolog.TraceLevel},
		{name: "separate flags add up", verbosity: []string{"-v", "-v", "-v"}, want: zerolog.TraceLevel},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			restoreGlobalLevel(t)
			zerolog.SetGlobalLevel(zerolog.PanicLevel)

			args := append([]string{"gospy", "--pyroscope", pyroscopeURL, "--app", appName}, tc.verbosity...)
			command := cli.New(func(context.Context, app.Config) error { return nil })
			command.Writer = io.Discard
			command.ErrWriter = io.Discard

			require.NoError(t, command.Run(append(args, "phpspy")))
			require.Equal(t, tc.want, zerolog.GlobalLevel())
		})
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	previous := log.Logger
	t.Cleanup(func() { log.Logger = previous })

	var logs bytes.Buffer
	log.Logger = zerolog.New(&logs)

	return &logs
}

func restoreGlobalLevel(t *testing.T) {
	t.Helper()

	level := zerolog.GlobalLevel()
	t.Cleanup(func() { zerolog.SetGlobalLevel(level) })
}
