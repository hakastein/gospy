package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
)

func writeProfilerScript(t *testing.T, relativePath string, body string) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(body), 0o755))
	return scriptPath
}

func TestRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		config  func(*testing.T) app.Config
		wantErr error
	}{
		{
			name: "allows negative stats interval to disable statistics",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					StatsInterval:    -time.Second,
				}
			},
			wantErr: nil,
		},
		{
			name: "allows zero stats interval to disable statistics",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					StatsInterval:    0,
				}
			},
			wantErr: nil,
		},
		{
			name: "returns error when profiler executable is missing",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      filepath.Join(t.TempDir(), "phpspy"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					StatsInterval:    time.Second,
				}
			},
			wantErr: errors.New("no such file or directory"),
		},
		{
			name: "accepts profiler path",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, filepath.Join("usr", "bin", "phpspy"), "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					StatsInterval:    time.Second,
				}
			},
			wantErr: nil,
		},
		{
			name: "returns profiler exit error",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 7\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					StatsInterval:    time.Second,
				}
			},
			wantErr: errors.New("exit status 7"),
		},
		{
			name: "rejects zero pyroscope workers",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 0,
				}
			},
			wantErr: errors.New("pyroscope workers must be at least 1"),
		},
		{
			name: "rejects negative pyroscope workers",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: -1,
				}
			},
			wantErr: errors.New("pyroscope workers must be at least 1"),
		},
		{
			name: "rejects an unparsable pyroscope url",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     "http://pyroscope.test:port",
					PyroscopeWorkers: 1,
				}
			},
			wantErr: errors.New(`invalid pyroscope url "http://pyroscope.test:port"`),
		},
		{
			name: "rejects a pyroscope url that is neither http nor https",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     "pyroscope.test:4040",
					PyroscopeWorkers: 1,
				}
			},
			wantErr: errors.New("pyroscope url must be http or https"),
		},
		{
			name: "rejects a missing pyroscope url",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeWorkers: 1,
				}
			},
			wantErr: errors.New("pyroscope url must be http or https"),
		},
		{
			name: "rejects a negative rate limit",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					RateMB:           -1,
					RateBurstMB:      1,
				}
			},
			wantErr: errors.New("pyroscope rate limit must not be negative"),
		},
		{
			name: "rejects a negative rate limit burst",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					RateMB:           1,
					RateBurstMB:      -1,
				}
			},
			wantErr: errors.New("pyroscope rate limit burst must not be negative"),
		},
		{
			name: "rejects a rate limit without a burst",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					RateMB:           1,
					RateBurstMB:      0,
				}
			},
			wantErr: errors.New("pyroscope rate limit burst must be above zero"),
		},
		{
			name: "accepts a zero rate limit as unlimited",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeURL:     pyroscopeURL,
					PyroscopeWorkers: 1,
					RateMB:           0,
					RateBurstMB:      0,
				}
			},
			wantErr: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := app.Run(context.Background(), tc.config(t))
			if tc.wantErr != nil {
				require.ErrorContains(t, err, tc.wantErr.Error())
				return
			}

			require.NoError(t, err)
		})
	}
}
