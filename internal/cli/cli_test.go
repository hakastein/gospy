package cli_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/cli"
)

const pyroscopeURL = "http://pyroscope.test"

// defaultConfig is what gospy runs with when only the required flag is given.
func defaultConfig() app.Config {
	return app.Config{
		PyroscopeURL:       pyroscopeURL,
		PyroscopeWorkers:   5,
		PyroscopeTimeout:   10 * time.Second,
		KeepEntrypointName: true,
		Restart:            "no",
		RateMB:             4,
		RateBurstMB:        6,
		StatsInterval:      10 * time.Second,
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
			name: "runs with defaults when only the required flag is given",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "phpspy"},
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
				"--app", "checkout",
				"--tag-entrypoint",
				"phpspy", "-P", "php-fpm", "--rate-hz", "99",
			},
			want: func(cfg *app.Config) {
				cfg.AppName = "checkout"
				cfg.TagEntrypoint = true
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{"-P", "php-fpm", "--rate-hz", "99"}
			},
		},
		{
			name: "flags after the profiler command belong to the profiler",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "phpspy", "--app", "not-gospy"},
			want: func(cfg *app.Config) {
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{"--app", "not-gospy"}
			},
		},
		{
			name: "entry point name is kept unless it is turned off",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--keep-entrypoint-name=false", "phpspy"},
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
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "phpspy"},
			env:  map[string]string{"GOSPY_PYROSCOPE_AUTH": "token-from-env"},
			want: func(cfg *app.Config) {
				cfg.PyroscopeAuth = "token-from-env"
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name: "pyroscope token on the command line wins over the environment",
			args: []string{"gospy", "--pyroscope", pyroscopeURL, "--pyroscope-auth", "token-from-flag", "phpspy"},
			env:  map[string]string{"GOSPY_PYROSCOPE_AUTH": "token-from-env"},
			want: func(cfg *app.Config) {
				cfg.PyroscopeAuth = "token-from-flag"
				cfg.ProfilerApp = "phpspy"
				cfg.ProfilerArguments = []string{}
			},
		},
		{
			name:    "invalid restart value is rejected",
			args:    []string{"gospy", "--pyroscope", pyroscopeURL, "--restart", "sometimes", "phpspy"},
			wantErr: "invalid restart option: sometimes",
		},
		{
			name:    "missing pyroscope url is rejected",
			args:    []string{"gospy", "phpspy"},
			wantErr: `Required flag "pyroscope" not set`,
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
