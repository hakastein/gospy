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
					ProfilerApp:   writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					StatsInterval: -time.Second,
					Restart:       "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "allows zero stats interval to disable statistics",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:   writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					StatsInterval: 0,
					Restart:       "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "returns error when profiler executable is missing",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:   filepath.Join(t.TempDir(), "phpspy"),
					StatsInterval: time.Second,
				}
			},
			wantErr: errors.New("no such file or directory"),
		},
		{
			name: "accepts profiler path",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:   writeProfilerScript(t, filepath.Join("usr", "bin", "phpspy"), "#!/bin/sh\nexit 0\n"),
					StatsInterval: time.Second,
					Restart:       "no",
				}
			},
			wantErr: nil,
		},
		{
			name: "returns profiler exit error",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:   writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 7\n"),
					StatsInterval: time.Second,
					Restart:       "no",
				}
			},
			wantErr: errors.New("exit status 7"),
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
