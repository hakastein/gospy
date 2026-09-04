package app_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/app"
)

const (
	pyroscopeURL      = "http://pyroscope.test"
	unthrottledRateMB = 100
)

type capturedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
}

type captureTransport struct {
	mu       sync.Mutex
	requests []capturedRequest
}

func (transport *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}

	transport.mu.Lock()
	transport.requests = append(transport.requests, capturedRequest{
		method: request.Method,
		path:   request.URL.Path,
		query:  request.URL.Query(),
		body:   string(body),
	})
	transport.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (transport *captureTransport) captured() []capturedRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()

	return append([]capturedRequest(nil), transport.requests...)
}

func replayProfiler(t *testing.T, fixture string) string {
	t.Helper()

	fixturePath, err := filepath.Abs(filepath.Join("testdata", "phpspy", fixture))
	require.NoError(t, err)
	require.FileExists(t, fixturePath)

	return writeProfilerScript(t, "phpspy", "#!/bin/sh\ncat "+fixturePath+"\n")
}

// The collector cuts a batch whenever its input looks empty, so one profiler session can
// arrive as several requests.
func countStacks(t *testing.T, requests []capturedRequest) map[string]map[string]int {
	t.Helper()

	profiles := make(map[string]map[string]int)
	for _, request := range requests {
		name := request.query.Get("name")
		if profiles[name] == nil {
			profiles[name] = make(map[string]int)
		}

		for _, line := range strings.Split(request.body, "\n") {
			separator := strings.LastIndex(line, " ")
			require.Greater(t, separator, 0, "folded line %q has no count", line)

			count, err := strconv.Atoi(line[separator+1:])
			require.NoError(t, err)
			profiles[name][line[:separator]] += count
		}
	}

	return profiles
}

func TestRunSendsProfilerOutputToPyroscope(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		fixture        string
		config         app.Config
		wantSampleRate string
		wantProfiles   map[string]map[string]int
	}{
		{
			name:    "peek-global meta lines become tags",
			fixture: "peek-global.txt",
			config: app.Config{
				AppName: "checkout",
				AppTags: []string{
					"env=production",
					`method={{ "glopeek server.REQUEST_METHOD" }}`,
					`uri={{ "glopeek server.REQUEST_URI" }}`,
				},
				KeepEntrypointName: true,
				ProfilerArguments: []string{
					"-P", "php-fpm",
					"-g", "server.REQUEST_URI",
					"-g", "server.REQUEST_METHOD",
					"-H", "137",
				},
			},
			wantSampleRate: "137",
			wantProfiles: map[string]map[string]int{
				"checkout{env=production,method=GET,uri=/orders/42}": {
					`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;App\Repository\OrderRepository::find;PDO::prepare`: 2,
					`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\OrderController::show;json_encode`:                                       1,
				},
				"checkout{env=production,method=POST,uri=/cart}": {
					`main /srv/app/public/index.php;App\Kernel::handle;App\Controller\CartController::add;usleep`: 1,
				},
			},
		},
		{
			name:    "request-info meta lines feed a rewritten tag and the entry point filter",
			fixture: "request-info.txt",
			config: app.Config{
				AppName: "checkout",
				AppTags: []string{
					"env=production",
					`route={{ "uri" "^/orders/[0-9]+$" "/orders/:id" }}`,
				},
				TagEntrypoint:      true,
				KeepEntrypointName: false,
				Entrypoints:        []string{"/srv/app/public/index.php"},
				ProfilerArguments: []string{
					"-P", "php-fpm",
					"-r", "qpc",
					"-m",
					"-H", "99",
				},
			},
			wantSampleRate: "99",
			wantProfiles: map[string]map[string]int{
				"checkout{env=production,route=/orders/:id,entrypoint=/srv/app/public/index.php}": {
					`main;App\Controller\OrderController::show;App\Repository\OrderRepository::find;mysqli::query`: 2,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureTransport{}

			cfg := tc.config
			cfg.ProfilerApp = replayProfiler(t, tc.fixture)
			cfg.PyroscopeURL = pyroscopeURL
			cfg.PyroscopeWorkers = 1
			cfg.RateMB = unthrottledRateMB
			cfg.RateBurstMB = unthrottledRateMB
			cfg.Transport = transport

			startedAt := time.Now()
			require.NoError(t, app.Run(context.Background(), cfg))
			finishedAt := time.Now()

			requests := transport.captured()
			require.NotEmpty(t, requests, "the profiler session produced no Pyroscope request")

			for _, request := range requests {
				require.Equal(t, http.MethodPost, request.method)
				require.Equal(t, "/ingest", request.path)
				require.Equal(t, "folded", request.query.Get("format"))
				require.Equal(t, tc.wantSampleRate, request.query.Get("sampleRate"))

				from, err := strconv.ParseInt(request.query.Get("from"), 10, 64)
				require.NoError(t, err)
				until, err := strconv.ParseInt(request.query.Get("until"), 10, 64)
				require.NoError(t, err)

				require.GreaterOrEqual(t, from, startedAt.Unix())
				require.GreaterOrEqual(t, until, from)
				require.LessOrEqual(t, until, finishedAt.Unix())
			}

			require.Equal(t, tc.wantProfiles, countStacks(t, requests))
		})
	}
}
