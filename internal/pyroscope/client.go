package pyroscope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"time"

	"github.com/rs/zerolog"

	"github.com/hakastein/gospy/internal/version"
)

const (
	// A Pyroscope reply carries a short status document; anything past this is a hostile
	// or broken server and is dropped rather than buffered.
	maxResponseBytes = 64 << 10
	// Errors become keys in the statistics report, so only a prefix of a body reaches them.
	maxErrorTextBytes = 256
)

type client struct {
	httpClient *http.Client
	url        *url.URL
	urlErr     error
	authToken  string
	logger     zerolog.Logger
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// statusError carries the status of a rejected send, so delivery can tell a server that is
// briefly unavailable from one that will refuse this batch however often it is offered.
type statusError struct {
	status     int
	retryAfter string
	message    string
}

func (err *statusError) Error() string {
	return err.message
}

type permanentError struct {
	err error
}

func (err *permanentError) Error() string {
	return err.err.Error()
}

func (err *permanentError) Unwrap() error {
	return err.err
}

func newClient(rawURL, authToken string, timeout time.Duration, transport http.RoundTripper, logger zerolog.Logger) *client {
	ingestURL, err := parseIngestURL(rawURL)

	return &client{
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// The Authorization header must never travel to a host the configuration did
			// not name, so a redirect is reported instead of followed.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		url:       ingestURL,
		urlErr:    err,
		authToken: authToken,
		logger:    logger,
	}
}

// parseIngestURL appends the ingest path to the configured URL, keeping the query it already carries.
func parseIngestURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid pyroscope url %q: %w", rawURL, err)
	}

	return parsed.JoinPath("ingest"), nil
}

func (client *client) send(ctx context.Context, profile payload) error {
	if client.urlErr != nil {
		return &permanentError{err: client.urlErr}
	}

	target := *client.url
	target.RawQuery = mergeQueries(client.url.RawQuery, profile.query)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(profile.body))
	if err != nil {
		return &permanentError{err: fmt.Errorf("error creating request: %w", err)}
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("gospy/%s/%s", version.Get(), runtime.Version()))
	if client.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+client.authToken)
	}

	client.logger.Debug().Str("query", httpReq.URL.RawQuery).Msg("requesting pyroscope")

	resp, err := client.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("http code: %d, error reading response: %w", resp.StatusCode, err)
	}

	return responseError(resp, responseBody)
}

func responseError(resp *http.Response, body []byte) error {
	switch {
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return nil
	case resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest:
		return newStatusError(resp, "http code: %d, redirect to %s not followed", resp.StatusCode, truncate(resp.Header.Get("Location")))
	}

	var result errorResponse
	if jsonParseErr := json.Unmarshal(body, &result); jsonParseErr != nil {
		return newStatusError(resp, "http code: %d, response isn't json: %s", resp.StatusCode, truncate(string(body)))
	}

	return newStatusError(resp, "http code: %d, error: %s, message: %s", resp.StatusCode, truncate(result.Code), truncate(result.Message))
}

func newStatusError(resp *http.Response, format string, args ...any) *statusError {
	return &statusError{
		status:     resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
		message:    fmt.Sprintf(format, args...),
	}
}

func mergeQueries(configured, profile string) string {
	if configured == "" {
		return profile
	}

	return configured + "&" + profile
}

func truncate(text string) string {
	if len(text) <= maxErrorTextBytes {
		return text
	}

	return text[:maxErrorTextBytes] + "..."
}
