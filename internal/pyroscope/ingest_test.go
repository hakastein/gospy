package pyroscope_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/version"
)

// unthrottledRateMB keeps the limiter out of the way of tests that are not about pacing.
const (
	unthrottledRateMB = 100
	bytesPerMegabyte  = 1 << 20
)

var (
	// singleAttempt keeps retrying out of the way of tests that are not about it, and
	// instantRetries retries with a zero ceiling on every wait, so no test sleeps.
	singleAttempt  = pyroscope.Retry{Attempts: 1}
	instantRetries = pyroscope.Retry{Attempts: 5}
)

type capturedRequest struct {
	method      string
	path        string
	header      http.Header
	query       url.Values
	body        string
	deadline    time.Time
	hasDeadline bool
}

type captureTransport struct {
	mu       sync.Mutex
	requests []capturedRequest
	respond  func(*http.Request) (*http.Response, error)
}

func (transport *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}

	deadline, hasDeadline := request.Context().Deadline()

	transport.mu.Lock()
	transport.requests = append(transport.requests, capturedRequest{
		method:      request.Method,
		path:        request.URL.Path,
		header:      request.Header.Clone(),
		query:       request.URL.Query(),
		body:        string(body),
		deadline:    deadline,
		hasDeadline: hasDeadline,
	})
	transport.mu.Unlock()

	if transport.respond != nil {
		return transport.respond(request)
	}

	return respondWith(http.StatusOK, ""), nil
}

func (transport *captureTransport) captured() []capturedRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()

	return append([]capturedRequest(nil), transport.requests...)
}

func respondWith(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type ingestOptions struct {
	cfg     pyroscope.Config
	level   zerolog.Level
	respond func(*http.Request) (*http.Response, error)
}

type ingestHarness struct {
	ingest    *pyroscope.Ingest
	transport *captureTransport
	logs      *bytes.Buffer
}

func startIngest(ctx context.Context, opts ingestOptions) *ingestHarness {
	transport := &captureTransport{respond: opts.respond}
	logs := &bytes.Buffer{}

	cfg := opts.cfg
	cfg.Transport = transport
	cfg.Logger = zerolog.New(zerolog.SyncWriter(logs)).Level(opts.level)
	if cfg.URL == "" {
		cfg.URL = "http://pyroscope.test"
	}
	if cfg.Workers == 0 {
		cfg.Workers = 1
	}
	if cfg.RateMB == 0 && cfg.RateBurstMB == 0 {
		cfg.RateMB = unthrottledRateMB
		cfg.RateBurstMB = unthrottledRateMB
	}
	if cfg.Retry == (pyroscope.Retry{}) {
		cfg.Retry = singleAttempt
	}

	return &ingestHarness{
		ingest:    pyroscope.StartIngest(ctx, cfg),
		transport: transport,
		logs:      logs,
	}
}

func (harness *ingestHarness) send(batches ...*collector.TagCollection) {
	for _, batch := range batches {
		harness.ingest.In() <- batch
	}

	close(harness.ingest.In())
	harness.ingest.Wait()
}

func (harness *ingestHarness) entries(t *testing.T, field, value string) []map[string]any {
	t.Helper()

	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(harness.logs.String()), "\n") {
		if line == "" {
			continue
		}

		entry := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "log line is not json: %s", line)
		if entry[field] == value {
			found = append(found, entry)
		}
	}

	return found
}

func (harness *ingestHarness) failures(t *testing.T) []map[string]any {
	t.Helper()

	return harness.entries(t, "level", "error")
}

func (harness *ingestHarness) reports(t *testing.T) []map[string]any {
	t.Helper()

	return harness.entries(t, "message", "pyroscope sending statistics")
}

func batch(tags string, stacks map[string]int) *collector.TagCollection {
	from := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

	return collector.NewTagCollection(from, from.Add(10*time.Second), tags, stacks)
}

func TestIngestSendsBatchOverTheWire(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		url       string
		authToken string
		wantPath  string
		wantQuery map[string]string
	}{
		{
			name:     "url without trailing slash",
			url:      "http://pyroscope.test",
			wantPath: "/ingest",
		},
		{
			name:     "url with trailing slash",
			url:      "http://pyroscope.test/",
			wantPath: "/ingest",
		},
		{
			name:      "url with a base path and a query",
			url:       "https://pyroscope.test/base?tenant=team-a",
			wantPath:  "/base/ingest",
			wantQuery: map[string]string{"tenant": "team-a"},
		},
		{
			name:      "with auth token",
			url:       "http://pyroscope.test",
			authToken: "secret-token",
			wantPath:  "/ingest",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					URL:        tc.url,
					AuthToken:  tc.authToken,
					AppName:    "test.app",
					StaticTags: "env=prod",
					SampleRate: 100,
				},
			})

			harness.send(batch("region=us-west", map[string]int{"main;foo": 1, "main;bar": 2}))

			requests := harness.transport.captured()
			require.Len(t, requests, 1)
			request := requests[0]

			assert.Equal(t, http.MethodPost, request.method)
			assert.Equal(t, tc.wantPath, request.path)
			assert.Equal(t, "folded", request.query.Get("format"), "the profile query must survive the join")
			for key, value := range tc.wantQuery {
				assert.Equal(t, value, request.query.Get(key), "the configured query must survive the join")
			}
			assert.Equal(t, "text/plain", request.header.Get("Content-Type"))
			assert.Equal(t, fmt.Sprintf("gospy/%s/%s", version.Get(), runtime.Version()), request.header.Get("User-Agent"))
			assert.ElementsMatch(t, []string{"main;foo 1", "main;bar 2"}, strings.Split(request.body, "\n"))

			if tc.authToken == "" {
				assert.Empty(t, request.header.Get("Authorization"))
			} else {
				assert.Equal(t, "Bearer "+tc.authToken, request.header.Get("Authorization"))
			}
		})
	}
}

func TestIngestComposesQuery(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		staticTags  string
		dynamicTags string
		expected    string
	}{
		{
			name:     "no tags",
			expected: "myapp{}",
		},
		{
			name:       "static tags only",
			staticTags: "env=prod",
			expected:   "myapp{env=prod}",
		},
		{
			name:        "dynamic tags only",
			dynamicTags: "user=admin",
			expected:    "myapp{user=admin}",
		},
		{
			name:        "static and dynamic tags",
			staticTags:  "env=prod",
			dynamicTags: "user=admin",
			expected:    "myapp{env=prod,user=admin}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:    "myapp",
					StaticTags: tc.staticTags,
					SampleRate: 42,
				},
			})

			sent := batch(tc.dynamicTags, map[string]int{"main;foo": 1})
			harness.send(sent)

			requests := harness.transport.captured()
			require.Len(t, requests, 1)
			query := requests[0].query

			assert.Equal(t, tc.expected, query.Get("name"))
			assert.Equal(t, fmt.Sprint(sent.From().Unix()), query.Get("from"))
			assert.Equal(t, fmt.Sprint(sent.Until().Unix()), query.Get("until"))
			assert.Equal(t, "42", query.Get("sampleRate"))
			assert.Equal(t, "folded", query.Get("format"))
		})
	}
}

func TestIngestReportsFailedSends(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		respond     func(*http.Request) (*http.Response, error)
		errContains string
	}{
		{
			name: "error response without body",
			respond: func(*http.Request) (*http.Response, error) {
				return respondWith(http.StatusForbidden, ""), nil
			},
			errContains: "http code: 403",
		},
		{
			name: "json error response",
			respond: func(*http.Request) (*http.Response, error) {
				return respondWith(http.StatusInternalServerError, `{"code":"internal_error","message":"something went wrong"}`), nil
			},
			errContains: "http code: 500, error: internal_error, message: something went wrong",
		},
		{
			name: "non json error response",
			respond: func(*http.Request) (*http.Response, error) {
				return respondWith(http.StatusBadRequest, "invalid request format"), nil
			},
			errContains: "response isn't json",
		},
		{
			name: "network error",
			respond: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial error")
			},
			errContains: "error sending request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:       "myapp",
					StatsInterval: time.Hour,
				},
				respond: tc.respond,
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			failures := harness.failures(t)
			require.Len(t, failures, 1)
			assert.Contains(t, failures[0]["error"], tc.errContains)

			reports := harness.reports(t)
			require.Len(t, reports, 1)
			assert.Equal(t, float64(1), reports[0]["failed_requests"])
			assert.Equal(t, float64(0), reports[0]["success_requests"])
		})
	}
}

func TestIngestAcceptsEverySuccessStatus(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "ok without body",
			status: http.StatusOK,
			body:   "",
		},
		{
			name:   "ok with body",
			status: http.StatusOK,
			body:   `{"status":"ok"}`,
		},
		{
			name:   "accepted with body",
			status: http.StatusAccepted,
			body:   "queued for ingestion",
		},
		{
			name:   "no content",
			status: http.StatusNoContent,
			body:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:       "myapp",
					StatsInterval: time.Hour,
				},
				respond: func(*http.Request) (*http.Response, error) {
					return respondWith(tc.status, tc.body), nil
				},
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			assert.Empty(t, harness.failures(t))

			reports := harness.reports(t)
			require.Len(t, reports, 1)
			assert.Equal(t, float64(1), reports[0]["success_requests"])
			assert.Equal(t, float64(0), reports[0]["failed_requests"])
		})
	}
}

func TestIngestDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			AuthToken:     "secret-token",
			StatsInterval: time.Hour,
		},
		respond: func(*http.Request) (*http.Response, error) {
			response := respondWith(http.StatusFound, "")
			response.Header.Set("Location", "http://elsewhere.test/ingest")

			return response, nil
		},
	})

	harness.send(batch("env=test", map[string]int{"main;foo": 1}))

	requests := harness.transport.captured()
	require.Len(t, requests, 1, "the redirect was followed, carrying the authorization header along")
	assert.Equal(t, "Bearer secret-token", requests[0].header.Get("Authorization"))

	failures := harness.failures(t)
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0]["error"], "http code: 302")
	assert.Contains(t, failures[0]["error"], "http://elsewhere.test/ingest")

	reports := harness.reports(t)
	require.Len(t, reports, 1)
	assert.Equal(t, float64(1), reports[0]["failed_requests"])
}

func TestIngestTruncatesOversizedErrorBodies(t *testing.T) {
	t.Parallel()

	const (
		hostileBodyBytes = 1 << 20
		reasonableError  = 1 << 10
	)

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
		},
		respond: func(*http.Request) (*http.Response, error) {
			return respondWith(http.StatusBadGateway, strings.Repeat("A", hostileBodyBytes)), nil
		},
	})

	harness.send(batch("env=test", map[string]int{"main;foo": 1}))

	failures := harness.failures(t)
	require.Len(t, failures, 1)
	failure, ok := failures[0]["error"].(string)
	require.True(t, ok, "the logged error is not a string")
	assert.Contains(t, failure, "http code: 502")
	assert.Less(t, len(failure), reasonableError, "the whole response body reached the log")

	reports := harness.reports(t)
	require.Len(t, reports, 1)
	errorKeys, ok := reports[0]["errors"].(map[string]any)
	require.True(t, ok, "the statistics report has no error map")
	require.Len(t, errorKeys, 1)
	for key := range errorKeys {
		assert.Less(t, len(key), reasonableError, "the whole response body became a statistics key")
	}
}

func TestIngestReportsAnUnparsableURL(t *testing.T) {
	t.Parallel()

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			URL:           "http://pyroscope.test:port",
			AppName:       "myapp",
			StatsInterval: time.Hour,
		},
	})

	harness.send(batch("env=test", map[string]int{"main;foo": 1}))

	assert.Empty(t, harness.transport.captured())

	failures := harness.failures(t)
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0]["error"], "invalid pyroscope url")
}

func TestIngestFlushesStatisticsOnInputEnd(t *testing.T) {
	t.Parallel()

	var attempts int
	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
		},
		respond: func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts == 2 {
				return respondWith(http.StatusForbidden, ""), nil
			}

			return respondWith(http.StatusOK, ""), nil
		},
	})

	harness.send(
		batch("env=test", map[string]int{"main;foo": 1}),
		batch("env=test", map[string]int{"main;foobar": 22}),
	)

	requests := harness.transport.captured()
	require.Len(t, requests, 2)

	reports := harness.reports(t)
	require.Len(t, reports, 1)
	assert.Equal(t, float64(2), reports[0]["total_requests"])
	assert.Equal(t, float64(1), reports[0]["success_requests"])
	assert.Equal(t, float64(1), reports[0]["failed_requests"])
	assert.Equal(t, float64(len(requests[0].body)+len(requests[1].body)), reports[0]["total_bytes"], "statistics must account the bytes actually sent")
	assert.Len(t, reports[0]["errors"], 1)
}

func TestIngestWaitIsIdempotent(t *testing.T) {
	t.Parallel()

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
		},
	})

	harness.send(batch("env=test", map[string]int{"main;foo": 1}))
	harness.ingest.Wait()

	require.Len(t, harness.reports(t), 1)
}

func TestIngestSkipsStatistics(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		interval time.Duration
		level    zerolog.Level
	}{
		{
			name:     "interval disabled",
			interval: 0,
			level:    zerolog.InfoLevel,
		},
		{
			name:     "negative interval",
			interval: -time.Second,
			level:    zerolog.InfoLevel,
		},
		{
			name:     "logger below info",
			interval: time.Hour,
			level:    zerolog.WarnLevel,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:       "myapp",
					StatsInterval: tc.interval,
				},
				level: tc.level,
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			require.Len(t, harness.transport.captured(), 1)
			require.Empty(t, harness.reports(t))
		})
	}
}

func TestIngestStartsAWorkerWhenNoneAreConfigured(t *testing.T) {
	t.Parallel()

	transport := &captureTransport{}
	ingest := pyroscope.StartIngest(context.Background(), pyroscope.Config{
		URL:         "http://pyroscope.test",
		AppName:     "myapp",
		RateMB:      unthrottledRateMB,
		RateBurstMB: unthrottledRateMB,
		Transport:   transport,
	})

	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		ingest.In() <- batch("env=test", map[string]int{"main;foo": 1})
		close(ingest.In())
		ingest.Wait()
	}()

	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("Ingest accepted no batch without a configured worker")
	}

	require.Len(t, transport.captured(), 1)
}

func TestIngestFallsBackToADefaultTimeout(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		timeout time.Duration
	}{
		{
			name:    "unset timeout",
			timeout: 0,
		},
		{
			name:    "negative timeout",
			timeout: -time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName: "myapp",
					Timeout: tc.timeout,
				},
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			requests := harness.transport.captured()
			require.Len(t, requests, 1)
			require.True(t, requests[0].hasDeadline, "request went out with no deadline at all")
			require.True(t, requests[0].deadline.After(time.Now()), "request went out with an expired deadline")
		})
	}
}

func TestIngestStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	harness := startIngest(ctx, ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
		},
	})

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		harness.send(batch("env=test", map[string]int{"main;foo": 1}))
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after context cancellation")
	}
}

func TestIngestDeliversUnderRateLimit(t *testing.T) {
	t.Parallel()

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:     "myapp",
			RateMB:      1,
			RateBurstMB: 1,
		},
	})

	harness.send(
		batch("env=test", map[string]int{"main;foo": 1}),
		batch("env=test", map[string]int{"main;bar": 2}),
	)

	require.Len(t, harness.transport.captured(), 2)
}

func respondInTurn(responses ...func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	var (
		mu   sync.Mutex
		next int
	)

	return func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		reply := responses[min(next, len(responses)-1)]
		next++
		mu.Unlock()

		return reply(request)
	}
}

func status(code int) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return respondWith(code, ""), nil
	}
}

func throttled(retryAfter string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		response := respondWith(http.StatusTooManyRequests, "")
		response.Header.Set("Retry-After", retryAfter)

		return response, nil
	}
}

func TestIngestRetriesTransientFailures(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		respond func(*http.Request) (*http.Response, error)
	}{
		{
			name:    "server error",
			respond: respondInTurn(status(http.StatusServiceUnavailable), status(http.StatusOK)),
		},
		{
			name: "network error",
			respond: respondInTurn(
				func(*http.Request) (*http.Response, error) { return nil, errors.New("dial error") },
				status(http.StatusOK),
			),
		},
		{
			name:    "throttled with a retry-after in seconds",
			respond: respondInTurn(throttled("1"), status(http.StatusOK)),
		},
		{
			name:    "throttled with a retry-after as an http date",
			respond: respondInTurn(throttled(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)), status(http.StatusOK)),
		},
		{
			name:    "throttled for an hour, capped by the policy",
			respond: respondInTurn(throttled("3600"), status(http.StatusOK)),
		},
		{
			name:    "throttled with an unreadable retry-after",
			respond: respondInTurn(throttled("soon"), status(http.StatusOK)),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:       "myapp",
					StatsInterval: time.Hour,
					Retry:         instantRetries,
				},
				respond: tc.respond,
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			requests := harness.transport.captured()
			require.Len(t, requests, 2, "the batch was not offered again")
			assert.Equal(t, requests[0].body, requests[1].body, "the retry sent a different batch")
			assert.Empty(t, harness.failures(t))

			reports := harness.reports(t)
			require.Len(t, reports, 1)
			assert.Equal(t, float64(1), reports[0]["total_requests"])
			assert.Equal(t, float64(1), reports[0]["success_requests"])
			assert.Equal(t, float64(1), reports[0]["retried_attempts"])
			assert.Equal(t, float64(0), reports[0]["failed_requests"])
		})
	}
}

func TestIngestNeverRetriesTerminalFailures(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		url          string
		respond      func(*http.Request) (*http.Response, error)
		wantRequests int
		errContains  string
	}{
		{
			name:         "bad request",
			respond:      status(http.StatusBadRequest),
			wantRequests: 1,
			errContains:  "http code: 400",
		},
		{
			name:         "forbidden",
			respond:      status(http.StatusForbidden),
			wantRequests: 1,
			errContains:  "http code: 403",
		},
		{
			name:         "not found",
			respond:      status(http.StatusNotFound),
			wantRequests: 1,
			errContains:  "http code: 404",
		},
		{
			name:         "redirect that was not followed",
			respond:      status(http.StatusFound),
			wantRequests: 1,
			errContains:  "http code: 302",
		},
		{
			name:         "url that does not parse",
			url:          "http://pyroscope.test:port",
			wantRequests: 0,
			errContains:  "invalid pyroscope url",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					URL:           tc.url,
					AppName:       "myapp",
					StatsInterval: time.Hour,
					Retry:         instantRetries,
				},
				respond: tc.respond,
			})

			harness.send(batch("env=test", map[string]int{"main;foo": 1}))

			assert.Len(t, harness.transport.captured(), tc.wantRequests)

			failures := harness.failures(t)
			require.Len(t, failures, 1)
			assert.Contains(t, failures[0]["error"], tc.errContains)

			reports := harness.reports(t)
			require.Len(t, reports, 1)
			assert.Equal(t, float64(1), reports[0]["failed_requests"])
			assert.Equal(t, float64(0), reports[0]["retried_attempts"])
			assert.Equal(t, float64(0), reports[0]["success_requests"])
		})
	}
}

func TestIngestGivesUpAfterTheConfiguredAttempts(t *testing.T) {
	t.Parallel()

	const attempts = 3

	harness := startIngest(context.Background(), ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
			Retry:         pyroscope.Retry{Attempts: attempts},
		},
		respond: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial error")
		},
	})

	harness.send(batch("env=test", map[string]int{"main;foo": 1}))

	assert.Len(t, harness.transport.captured(), attempts)

	failures := harness.failures(t)
	require.Len(t, failures, 1, "giving up on a batch must be reported once, not once per attempt")
	assert.Contains(t, failures[0]["error"], "dial error")

	reports := harness.reports(t)
	require.Len(t, reports, 1)
	assert.Equal(t, float64(1), reports[0]["failed_requests"])
	assert.Equal(t, float64(attempts-1), reports[0]["retried_attempts"])
}

func TestIngestPacesBatchesLargerThanTheBurst(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		rateMB float64
	}{
		{
			name:   "under a rate limit",
			rateMB: unthrottledRateMB,
		},
		{
			name:   "unlimited",
			rateMB: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			harness := startIngest(context.Background(), ingestOptions{
				cfg: pyroscope.Config{
					AppName:       "myapp",
					StatsInterval: time.Hour,
					RateMB:        tc.rateMB,
					RateBurstMB:   4.0 / bytesPerMegabyte,
				},
			})

			harness.send(
				batch("env=test", map[string]int{"main;foo": 1}),
				batch("env=test", map[string]int{"main;bar": 2}),
				batch("env=test", map[string]int{"main;baz": 3}),
			)

			requests := harness.transport.captured()
			require.Len(t, requests, 3, "a batch larger than the burst was dropped for its size")
			for _, request := range requests {
				assert.Greater(t, len(request.body), 4, "the test batch is not larger than the burst")
			}

			reports := harness.reports(t)
			require.Len(t, reports, 1)
			assert.Equal(t, float64(3), reports[0]["success_requests"])
		})
	}
}

func TestIngestStopsRetryingOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempted := make(chan struct{}, 1)
	harness := startIngest(ctx, ingestOptions{
		cfg: pyroscope.Config{
			AppName:       "myapp",
			StatsInterval: time.Hour,
			Retry:         pyroscope.Retry{Attempts: 5, BaseDelay: time.Hour, MaxDelay: time.Hour},
		},
		respond: func(*http.Request) (*http.Response, error) {
			select {
			case attempted <- struct{}{}:
			default:
			}

			return respondWith(http.StatusServiceUnavailable, ""), nil
		},
	})

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		harness.send(batch("env=test", map[string]int{"main;foo": 1}))
	}()

	<-attempted
	cancel()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return while a retry was waiting out its backoff")
	}

	assert.Len(t, harness.transport.captured(), 1)
}
