package pyroscope

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"net/url"
)

// Client handles sending data to Pyroscope with rate limiting.
type Client struct {
	httpClient *http.Client
	url        string
	auth       string
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewClient initializes and returns a new Client.
func NewClient(
	url string,
	auth string,
	httpClient *http.Client,
) *Client {
	return &Client{
		httpClient: httpClient,
		url:        url + "/ingest",
		auth:       auth,
	}
}

// Send sends the TagCollection data to Pyroscope and returns the HTTP status code and any error encountered.
func (cl *Client) Send(
	ctx context.Context,
	ingestData IngestData,
) (int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", cl.url, ingestData.getBody())
	if err != nil {
		return 0, fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	if cl.auth != "" {
		httpReq.Header.Set("Authorization", cl.auth)
	}

	httpReq.URL.RawQuery = ingestData.MakeQuery()

	unescaped, _ := url.QueryUnescape(httpReq.URL.RawQuery)
	log.Debug().Str("query", unescaped).Msg("requesting pyroscope")

	resp, err := cl.httpClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var result ErrorResponse
		responseBody, _ := io.ReadAll(resp.Body)
		jsonParseErr := json.Unmarshal(responseBody, &result)
		if jsonParseErr != nil {
			return resp.StatusCode, fmt.Errorf("response isn't json: %s", responseBody)
		}
		return resp.StatusCode, fmt.Errorf("http code: %s, error: %s, message: %s", resp.Status, result.Code, result.Message)
	}

	return resp.StatusCode, nil
}
