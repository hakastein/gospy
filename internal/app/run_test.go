package app_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
					PyroscopeURL:     "http://pyroscope.test:port",
					PyroscopeWorkers: 1,
				}
			},
			wantErr: errors.New(`invalid pyroscope url "http://pyroscope.test:port"`),
		},
		{
			name: "rejects a missing pyroscope url",
			config: func(t *testing.T) app.Config {
				return app.Config{
					ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\nexit 0\n"),
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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
					AppName:          "checkout",
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

func TestRunRejectsAnInvalidConfigurationBeforeStartingTheProfiler(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		config  func(app.Config) app.Config
		wantErr string
	}{
		{
			name: "missing app name",
			config: func(cfg app.Config) app.Config {
				cfg.AppName = ""
				return cfg
			},
			wantErr: "no app name specified",
		},
		{
			name: "unknown restart policy",
			config: func(cfg app.Config) app.Config {
				cfg.Restart = "sometimes"
				return cfg
			},
			wantErr: "invalid restart option: sometimes",
		},
		{
			name: "pyroscope url without a scheme",
			config: func(cfg app.Config) app.Config {
				cfg.PyroscopeURL = "pyroscope.test:4040"
				return cfg
			},
			wantErr: "pyroscope url must be http or https",
		},
		{
			name: "sleep interval longer than one second",
			config: func(cfg app.Config) app.Config {
				cfg.ProfilerArguments = []string{"--sleep-ns", "1500000000"}
				return cfg
			},
			wantErr: "sleep interval 1500000000 ns is longer than one second",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			started := filepath.Join(t.TempDir(), "profiler-started")
			cfg := tc.config(app.Config{
				ProfilerApp:      writeProfilerScript(t, "phpspy", "#!/bin/sh\ntouch "+started+"\n"),
				AppName:          "checkout",
				PyroscopeURL:     pyroscopeURL,
				PyroscopeWorkers: 1,
			})

			err := app.Run(context.Background(), cfg)

			require.ErrorContains(t, err, tc.wantErr)
			require.NoFileExists(t, started, "profiler started despite an invalid configuration")
		})
	}
}

func TestRunEndsTheSessionOnAnOverLongProfilerLine(t *testing.T) {
	t.Parallel()

	bodies := make(chan string, 1)
	pyroscope := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		select {
		case bodies <- string(body):
		default:
		}
	}))
	defer pyroscope.Close()

	// 2 MB is past the profiler's stdout cap; the script then stays alive holding the pipe's write end.
	longLine := filepath.Join(t.TempDir(), "long-line")
	require.NoError(t, os.WriteFile(longLine, append(bytes.Repeat([]byte{'x'}, 2<<20), '\n'), 0o644))

	cfg := app.Config{
		ProfilerApp: writeProfilerScript(t, "phpspy",
			"#!/bin/sh\nprintf '0 func1 /app/helper.php:10\\n1 main /app/index.php:1\\n\\n'\ncat "+longLine+"\nsleep 60\n"),
		AppName:          "checkout",
		PyroscopeURL:     pyroscope.URL,
		PyroscopeWorkers: 1,
		RateMB:           1,
		RateBurstMB:      1,
	}

	done := make(chan error, 1)
	go func() {
		done <- app.Run(context.Background(), cfg)
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, bufio.ErrTooLong)
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the profiler emitted an over-long stdout line")
	}

	select {
	case body := <-bodies:
		require.Contains(t, body, "main;func1")
	default:
		t.Fatal("the sample parsed before the over-long line never reached pyroscope")
	}
}

func TestRunEndsTheSessionWhenTheProfilerExitsBeforeItsChildren(t *testing.T) {
	t.Parallel()

	transport := &captureTransport{}
	// sleep stands in for a pgrep-mode child: it inherits phpspy's stdout and outlives phpspy.
	cfg := checkoutConfig(writeProfilerScript(t, "phpspy", "#!/bin/sh\nsleep 60 &\ncat "+fixturePath(t, "peek-global.txt")+"\n"))
	cfg.PyroscopeURL = pyroscopeURL
	cfg.Transport = transport

	require.NoError(t, awaitRun(t, startRun(context.Background(), cfg)))

	require.Equal(t, peekGlobalCheckoutStacks(), countStacks(t, transport.captured()), "every sample printed before the profiler exited must be delivered")
}
