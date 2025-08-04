package pyroscope

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/version"
)

// Client handles sending data to Pyroscope server.
type Client struct {
	httpClient *http.Client
	url        string
	authToken  string
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewClient initializes and returns a new Client.
func NewClient(
	url string,
	authToken string,
	httpClient *http.Client,
) *Client {
	return &Client{
		httpClient: httpClient,
		url:        strings.TrimSuffix(url, "/") + "/ingest",
		authToken:  authToken,
	}
}

// Send sends the profile data to Pyroscope and returns the HTTP status code and any error encountered.
func (client *Client) Send(
	ctx context.Context,
	payload Payload,
) error {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", client.url, payload.BodyReader())
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("gospy/%s/%s", version.Get(), runtime.Version()))
	if client.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer"+client.authToken)
	}

	httpReq.URL.RawQuery = payload.QueryString()

	log.Debug().Str("query", httpReq.URL.RawQuery).Msg("requesting pyroscope")

	resp, err := client.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result ErrorResponse
		responseBody, _ := io.ReadAll(resp.Body)
		jsonParseErr := json.Unmarshal(responseBody, &result)
		if jsonParseErr != nil {
			return fmt.Errorf("response isn't json: %s", responseBody)
		}
		return fmt.Errorf("http code: %s, error: %s, message: %s", resp.Status, result.Code, result.Message)
	}

	return nil
}
