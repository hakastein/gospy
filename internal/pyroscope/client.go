package pyroscope

import (
	"context"
	"fmt"
	"golang.org/x/time/rate"
	"net/http"
	"time"
)

type Client struct {
	httpClient *http.Client
	limiter    *rate.Limiter
	url        string
	auth       string
}

func (cl *Client) send(ctx context.Context, request *requestData) error {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", cl.url+"/ingest", &request.data)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	if cl.auth != "" {
		httpReq.Header.Set("Authorization", cl.auth)
	}

	httpReq.URL.RawQuery = request.String()

	resp, err := cl.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received unexpected response code: %s", resp.Status)
	}

	return nil
}

func NewClient(url string, auth string, timeout time.Duration, byteLimit int, byteBurst int) *Client {
	return &Client{
		limiter: rate.NewLimiter(rate.Limit(byteLimit), byteBurst),
		httpClient: &http.Client{
			Timeout: timeout * time.Second,
		},
		url:  url,
		auth: auth,
	}
}
