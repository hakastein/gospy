package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/cli"
)

const configuration = `
pyroscope:
  url: http://pyroscope.test
app: checkout
instance-name: gospy-test
targets:
  - name: fpm
    match: { comm: php-fpm }
    max-processes: 5
    rate: 25
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "gospy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	return path
}

type runnerDouble struct {
	started bool
	cfg     app.Config
}

func (double *runnerDouble) run(_ context.Context, cfg app.Config) error {
	double.started = true
	double.cfg = cfg

	return nil
}

func runCommand(t *testing.T, args ...string) (*runnerDouble, error) {
	t.Helper()

	double := &runnerDouble{}
	command := cli.New(double.run)
	command.Writer = io.Discard
	command.ErrWriter = io.Discard

	return double, command.Run(append([]string{"gospy"}, args...))
}

func TestNewLoadsTheConfigurationNamedOnTheCommandLine(t *testing.T) {
	restoreGlobalLevel(t)
	t.Setenv("GOSPY_PYROSCOPE_AUTH", "token-from-env")

	double, err := runCommand(t, "--config", writeConfig(t, configuration))
	require.NoError(t, err)
	require.True(t, double.started)

	require.Equal(t, "checkout", double.cfg.App)
	require.Equal(t, "http://pyroscope.test", double.cfg.Pyroscope.URL)
	require.Equal(t, "token-from-env", double.cfg.Pyroscope.Auth, "the token comes from the environment")
	require.Equal(t, 10*time.Second, double.cfg.Pyroscope.Timeout, "defaults are applied by the loader")
	require.Len(t, double.cfg.Targets, 1)
	require.Equal(t, "fpm", double.cfg.Targets[0].Name)
	require.Equal(t, 25, double.cfg.Targets[0].Rate)
	require.Nil(t, double.cfg.Transport, "the command line never injects a transport")
	require.Nil(t, double.cfg.Processes, "the command line never injects a process source")
}

func TestNewReadsThePathFromTheEnvironment(t *testing.T) {
	restoreGlobalLevel(t)

	t.Setenv("GOSPY_CONFIG", writeConfig(t, configuration))

	double, err := runCommand(t)
	require.NoError(t, err)
	require.True(t, double.started)
	require.Equal(t, "checkout", double.cfg.App)
}

func TestNewPrefersTheFlagOverTheEnvironment(t *testing.T) {
	restoreGlobalLevel(t)

	t.Setenv("GOSPY_CONFIG", writeConfig(t, "pyroscope: [\n"))

	double, err := runCommand(t, "--config", writeConfig(t, configuration))
	require.NoError(t, err)
	require.True(t, double.started)
}

func TestNewReadsTheDefaultPathWhenNothingIsNamed(t *testing.T) {
	restoreGlobalLevel(t)

	double, err := runCommand(t)
	if _, statErr := os.Stat(cli.DefaultConfigPath); os.IsNotExist(statErr) {
		require.ErrorContains(t, err, cli.DefaultConfigPath)
		require.False(t, double.started)
		return
	}

	t.Skipf("%s exists on this machine", cli.DefaultConfigPath)
}

func TestNewRejectsWhatItCannotRun(t *testing.T) {
	restoreGlobalLevel(t)

	testCases := []struct {
		name    string
		args    func(t *testing.T) []string
		wantErr string
	}{
		{
			name: "a positional argument",
			args: func(t *testing.T) []string {
				return []string{"--config", writeConfig(t, configuration), "phpspy"}
			},
			wantErr: `unexpected argument "phpspy": gospy takes no positional arguments, the configuration lives in the file named by --config`,
		},
		{
			name: "a flag of the old command line",
			args: func(t *testing.T) []string {
				return []string{"--config", writeConfig(t, configuration), "--pyroscope", "http://pyroscope.test"}
			},
			wantErr: "flag provided but not defined: -pyroscope",
		},
		{
			name: "a missing file",
			args: func(t *testing.T) []string {
				return []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}
			},
			wantErr: "cannot read configuration",
		},
		{
			name: "a broken file",
			args: func(t *testing.T) []string {
				return []string{"--config", writeConfig(t, "pyroscope: { url: http://pyroscope.test }\napp: checkout\n")}
			},
			wantErr: "targets is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			double, err := runCommand(t, tc.args(t)...)
			require.ErrorContains(t, err, tc.wantErr)
			require.False(t, double.started, "the runtime started despite a bad command line")
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

			args := append([]string{"--config", writeConfig(t, configuration)}, tc.verbosity...)
			double, err := runCommand(t, args...)
			require.NoError(t, err)
			require.True(t, double.started)
			require.Equal(t, tc.want, zerolog.GlobalLevel())
		})
	}
}

func TestNewPrintsTheVersionWithoutAConfiguration(t *testing.T) {
	double, err := runCommand(t, "--version")
	require.NoError(t, err)
	require.False(t, double.started)
}

func restoreGlobalLevel(t *testing.T) {
	t.Helper()

	level := zerolog.GlobalLevel()
	t.Cleanup(func() { zerolog.SetGlobalLevel(level) })
}
