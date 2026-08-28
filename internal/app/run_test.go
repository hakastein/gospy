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
					PyroscopeWorkers: 1,
					StatsInterval:    -time.Second,
					Restart:          "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "allows zero stats interval to disable statistics",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeWorkers: 1,
					StatsInterval:    0,
					Restart:          "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "returns error when profiler executable is missing",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      filepath.Join(t.TempDir(), "phpspy"),
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
					PyroscopeWorkers: 1,
					StatsInterval:    time.Second,
					Restart:          "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "returns profiler exit error",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 7\n"),
					PyroscopeWorkers: 1,
					StatsInterval:    time.Second,
					Restart:          "no",
				}
			},
			wantErr: errors.New("exit status 7"),
		},
		{
			name: "rejects zero pyroscope workers",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeWorkers: 0,
					Restart:          "no",
				}
			},
			wantErr: errors.New("pyroscope workers must be at least 1"),
		},
		{
			name: "rejects negative pyroscope workers",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					PyroscopeWorkers: -1,
					Restart:          "no",
				}
			},
			wantErr: errors.New("pyroscope workers must be at least 1"),
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

func TestRunDrainsEmittedTracesWithoutHanging(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		ProfilerApp: writeProfilerScript(t, "phpspy",
			"#!/bin/sh\nprintf '0 func1 /app/helper.php:10\\n1 main /app/index.php:1\\n\\n'\nexit 0\n"),
		PyroscopeWorkers: 1,
		Restart:          "no",
		RateMB:           1,
		RateBurstMB:      1,
	}

	done := make(chan error, 1)
	go func() {
		done <- app.Run(context.Background(), cfg)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the profiler emitted a trace and exited")
	}
}
