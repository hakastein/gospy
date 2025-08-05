package pyroscope_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/version"
)

// assertValidRequest is a helper function to validate the HTTP request sent to Pyroscope.
func assertValidRequest(t *testing.T, r *http.Request, p pyroscope.Payload, authToken string) {
	t.Helper()

	expectedUserAgent := fmt.Sprintf("gospy/%s/%s", version.Get(), runtime.Version())
	assert.Equal(t, "POST", r.Method)
	assert.Equal(t, "/ingest", r.URL.Path)
	assert.Equal(t, "text/plain", r.Header.Get("Content-Type"))
	assert.Equal(t, expectedUserAgent, r.Header.Get("User-Agent"))
	assert.Equal(t, p.QueryString(), r.URL.RawQuery)

	if authToken != "" {
		assert.Equal(t, "Bearer "+authToken, r.Header.Get("Authorization"))
	} else {
		assert.Empty(t, r.Header.Get("Authorization"))
	}

	// Read the actual body from the request
	actualBody, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	// Read the expected body from the payload
	expectedBodyReader := p.BodyReader()
	expectedBody, err := io.ReadAll(expectedBodyReader)
	require.NoError(t, err)

	// Compare bodies by lines, as order is not guaranteed.
	actualBodyLines := strings.Split(strings.TrimSpace(string(actualBody)), "\n")
	expectedBodyLines := strings.Split(strings.TrimSpace(string(expectedBody)), "\n")
	assert.ElementsMatch(t, expectedBodyLines, actualBodyLines)
}

func TestNewClient(t *testing.T) {
	t.Run("url formatting correctly handles trailing slashes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/ingest", r.URL.Path, "request path should always be /ingest")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		payload := pyroscope.NewAppMetadata("app", "", 100).NewPayload(
			collector.NewTagCollection(time.Now(), time.Now(), "", nil),
		)

		tests := []struct {
			name     string
			inputURL string
		}{
			{
				name:     "url with trailing slash",
				inputURL: server.URL + "/",
			},
			{
				name:     "url without trailing slash",
				inputURL: server.URL,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				client := pyroscope.NewClient(tt.inputURL, "", server.Client())
				err := client.Send(context.Background(), payload)
				require.NoError(t, err)
			})
		}
	})
}

func TestClient_Send(t *testing.T) {
	now := time.Now()
	tagData := collector.NewTagCollection(
		now,
		now.Add(10*time.Second),
		"region=us-west",
		map[string]int{
			"main;foo": 1,
			"main;bar": 2,
		},
	)
	meta := pyroscope.NewAppMetadata("test.app", "env=prod", 100)
	testPayload := meta.NewPayload(tagData)

	t.Run("successful requests", func(t *testing.T) {
		tests := []struct {
			name      string
			authToken string
		}{
			{
				name:      "with auth token",
				authToken: "secret-token",
			},
			{
				name:      "without auth token",
				authToken: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assertValidRequest(t, r, testPayload, tt.authToken)
					w.WriteHeader(http.StatusOK)
				})
				server := httptest.NewServer(handler)
				defer server.Close()

				client := pyroscope.NewClient(server.URL, tt.authToken, server.Client())
				err := client.Send(context.Background(), testPayload)
				require.NoError(t, err)
			})
		}
	})

	t.Run("server errors", func(t *testing.T) {
		tests := []struct {
			name        string
			handler     http.HandlerFunc
			errContains string
		}{
			{
				name: "non ok code",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusForbidden)
				},
				errContains: "http code: 403",
			},
			{
				name: "ok code with non empty response",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`Something went wrong`))
				},
				errContains: "server has returned body with 200 ok",
			},
			{
				name: "json response",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, err := w.Write([]byte(`{"code":"internal_error","message":"something went wrong"}`))
					require.NoError(t, err)
				},
				errContains: "http code: 500, error: internal_error, message: something went wrong",
			},
			{
				name: "non-json error",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte("invalid request format"))
				},
				errContains: "response isn't json",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				server := httptest.NewServer(tt.handler)
				defer server.Close()

				client := pyroscope.NewClient(server.URL, "", server.Client())
				err := client.Send(context.Background(), testPayload)

				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			})
		}
	})

	t.Run("network error on send", func(t *testing.T) {
		server := httptest.NewServer(nil)
		url := server.URL
		server.Close()

		client := pyroscope.NewClient(url, "", http.DefaultClient)
		err := client.Send(context.Background(), testPayload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "error sending request")
	})

	t.Run("context canceled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		client := pyroscope.NewClient(server.URL, "", server.Client())
		err := client.Send(ctx, testPayload)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})
}
