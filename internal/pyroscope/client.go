package pyroscope

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/hakastein/gospy/internal/version"
)

type client struct {
	httpClient *http.Client
	url        string
	authToken  string
	logger     zerolog.Logger
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newClient(url, authToken string, timeout time.Duration, transport http.RoundTripper, logger zerolog.Logger) *client {
	return &client{
		httpClient: &http.Client{Timeout: timeout, Transport: transport},
		url:        strings.TrimSuffix(url, "/") + "/ingest",
		authToken:  authToken,
		logger:     logger,
	}
}

func (client *client) send(ctx context.Context, profile payload) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, client.url, bytes.NewReader(profile.body))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("gospy/%s/%s", version.Get(), runtime.Version()))
	if client.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+client.authToken)
	}

	httpReq.URL.RawQuery = profile.query

	client.logger.Debug().Str("query", httpReq.URL.RawQuery).Msg("requesting pyroscope")

	resp, err := client.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK && len(responseBody) != 0 {
		return fmt.Errorf("server has returned body with 200 ok")
	}

	if resp.StatusCode != http.StatusOK {
		var result errorResponse
		jsonParseErr := json.Unmarshal(responseBody, &result)
		if jsonParseErr != nil {
			return fmt.Errorf("http code: %d, response isn't json: %s", resp.StatusCode, responseBody)
		}
		return fmt.Errorf("http code: %d, error: %s, message: %s", resp.StatusCode, result.Code, result.Message)
	}

	return nil
}
