package pyroscope

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client handles sending data to Pyroscope with rate limiting.
type Client struct {
	httpClient *http.Client
	url        string
	auth       string
	app        string
	staticTags string
	ctx        context.Context
	rateHz     int
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewClient initializes and returns a new Client.
func NewClient(
	ctx context.Context,
	url string,
	auth string,
	app string,
	staticTags string,
	rateHz int,
	timeout time.Duration,
) *Client {
	return &Client{
		ctx: ctx,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		staticTags: staticTags,
		app:        app,
		url:        url,
		auth:       auth,
		rateHz:     rateHz,
	}
}

// send sends the TagCollection data to Pyroscope and returns the HTTP status code and any error encountered.
func (cl *Client) send(tags string, from time.Time, until time.Time, body io.Reader) (int, error) {
	httpReq, err := http.NewRequestWithContext(cl.ctx, "POST", cl.url+"/ingest", body)
	if err != nil {
		return 0, fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	if cl.auth != "" {
		httpReq.Header.Set("Authorization", cl.auth)
	}

	httpReq.URL.RawQuery = makeQuery(makeAppName(cl.app, cl.staticTags, tags), from, until, cl.rateHz)

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

func makeAppName(appName string, staticTags string, dynamicTags string) string {
	var builder strings.Builder

	builder.WriteString(appName)
	builder.WriteString("{")
	if staticTags != "" {
		builder.WriteString(staticTags)
		builder.WriteString(",")
	}
	if dynamicTags != "" {
		builder.WriteString(dynamicTags)
	}
	builder.WriteString("}")

	return builder.String()
}

func makeQuery(name string, from time.Time, until time.Time, rateHz int) string {
	var builder strings.Builder

	builder.WriteString("name=")
	builder.WriteString(url.QueryEscape(name))
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(from.Unix(), 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(until.Unix(), 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(rateHz))
	builder.WriteString("&format=folded")

	return builder.String()
}
