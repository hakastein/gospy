package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
)

const shutdownSafetyNet = 10 * time.Second

func checkoutConfig(profilerApp string) app.Config {
	return app.Config{
		ProfilerApp:        profilerApp,
		AppName:            "checkout",
		AppTags:            []string{"env=production", `uri={{ "glopeek server.REQUEST_URI" }}`},
		KeepEntrypointName: true,
		ProfilerArguments:  []string{"-P", "php-fpm", "-g", "server.REQUEST_URI", "-H", "99"},
		PyroscopeWorkers:   1,
		RateMB:             unthrottledRateMB,
		RateBurstMB:        unthrottledRateMB,
	}
}

func hangingPyroscope(t *testing.T) (string, <-chan struct{}) {
	t.Helper()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}

		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	return server.URL, started
}

func startRun(ctx context.Context, cfg app.Config) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, cfg)
	}()

	return done
}

func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(shutdownSafetyNet):
		t.Fatal("Run did not return in time")
		return nil
	}
}

func awaitSignal(t *testing.T, ready <-chan struct{}, failure string) {
	t.Helper()

	select {
	case <-ready:
	case <-time.After(shutdownSafetyNet):
		t.Fatal(failure)
	}
}

func TestRunDeliversEveryAcceptedSampleOnCancellation(t *testing.T) {
	t.Parallel()

	consumed := filepath.Join(t.TempDir(), "consumed")
	require.NoError(t, syscall.Mkfifo(consumed, 0o600))

	// A megabyte of whitespace lines outruns the pipe and scanner buffers: once the script is
	// past it, the parser has queued every sample printed before.
	profiler := writeProfilerScript(t, "phpspy", "#!/bin/sh\n"+
		"cat "+fixturePath(t, "peek-global.txt")+"\n"+
		"yes \"$(printf '%1023s' '')\" | head -n 1024\n"+
		"echo > "+consumed+"\n"+
		"exec sleep 60\n")

	transport := &captureTransport{}
	cfg := checkoutConfig(profiler)
	cfg.PyroscopeURL = pyroscopeURL
	cfg.BatchInterval = time.Minute
	cfg.Transport = transport

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startRun(ctx, cfg)

	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = os.ReadFile(consumed)
	}()
	awaitSignal(t, read, "the profiler never got past its output")

	cancel()
	require.NoError(t, awaitRun(t, done), "a shutdown is not a failed run")

	require.Equal(t, map[string]map[string]int{
		"checkout{env=production,uri=/orders/42}": {
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;App\Repository\OrderRepository::find;PDO::prepare`: 2,
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;json_encode`:                                       1,
		},
		"checkout{env=production,uri=/cart}": {
			`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\CartController::add;usleep`: 1,
		},
	}, countStacks(t, transport.captured()), "every sample accepted before cancellation must be delivered")
}

func TestRunEndsTheDrainAtItsDeadline(t *testing.T) {
	t.Parallel()

	url, started := hangingPyroscope(t)

	cfg := checkoutConfig(writeProfilerScript(t, "phpspy", "#!/bin/sh\ncat "+fixturePath(t, "peek-global.txt")+"\nexec sleep 60\n"))
	cfg.PyroscopeURL = url
	cfg.PyroscopeTimeout = time.Minute
	cfg.BatchInterval = 10 * time.Millisecond
	cfg.DrainTimeout = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startRun(ctx, cfg)
	awaitSignal(t, started, "no batch reached pyroscope")

	cancel()
	require.NoError(t, awaitRun(t, done), "a drain cut short by its deadline is not a failed run")
}

// Not parallel: a signal sent to the test process reaches every Run listening at the time.
func TestRunAbortsTheDrainOnASecondSignal(t *testing.T) {
	testCases := []struct {
		name     string
		shutdown func(t *testing.T, cancel context.CancelFunc)
	}{
		{
			name: "cancelled context, then SIGTERM",
			shutdown: func(t *testing.T, cancel context.CancelFunc) {
				cancel()
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
			},
		},
		{
			name: "SIGTERM, then SIGINT",
			shutdown: func(t *testing.T, _ context.CancelFunc) {
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, started := hangingPyroscope(t)

			cfg := checkoutConfig(writeProfilerScript(t, "phpspy", "#!/bin/sh\ncat "+fixturePath(t, "peek-global.txt")+"\nexec sleep 60\n"))
			cfg.PyroscopeURL = url
			cfg.PyroscopeTimeout = time.Minute
			cfg.BatchInterval = 10 * time.Millisecond
			cfg.DrainTimeout = time.Minute

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := startRun(ctx, cfg)
			awaitSignal(t, started, "no batch reached pyroscope")

			tc.shutdown(t, cancel)
			require.ErrorIs(t, awaitRun(t, done), app.ErrDrainAborted)
		})
	}
}
