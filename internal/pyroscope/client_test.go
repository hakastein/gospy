package pyroscope_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func testPayload() pyroscope.Payload {
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
	return meta.NewPayload(tagData)
}

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

	actualBody, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	expectedBodyReader := p.BodyReader()
	expectedBody, err := io.ReadAll(expectedBodyReader)
	require.NoError(t, err)

	actualBodyLines := strings.Split(strings.TrimSpace(string(actualBody)), "\n")
	expectedBodyLines := strings.Split(strings.TrimSpace(string(expectedBody)), "\n")
	assert.ElementsMatch(t, expectedBodyLines, actualBodyLines)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestNewClient(t *testing.T) {
	payload := pyroscope.NewAppMetadata("app", "", 100).NewPayload(
		collector.NewTagCollection(time.Now(), time.Now(), "", nil),
	)

	tests := []struct {
		name     string
		inputURL string
	}{
		{
			name:     "url with trailing slash",
			inputURL: "http://pyroscope.test/",
		},
		{
			name:     "url without trailing slash",
			inputURL: "http://pyroscope.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := pyroscope.NewClient(tt.inputURL, "", newTestClient(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, "/ingest", r.URL.Path)
				return response(http.StatusOK, ""), nil
			}))

			err := client.Send(context.Background(), payload)
			require.NoError(t, err)
		})
	}
}

func TestClient_Send(t *testing.T) {
	payload := testPayload()

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
				client := pyroscope.NewClient("http://pyroscope.test", tt.authToken, newTestClient(func(r *http.Request) (*http.Response, error) {
					assertValidRequest(t, r, payload, tt.authToken)
					return response(http.StatusOK, ""), nil
				}))

				err := client.Send(context.Background(), payload)
				require.NoError(t, err)
			})
		}
	})

	t.Run("server errors", func(t *testing.T) {
		tests := []struct {
			name        string
			resp        *http.Response
			errContains string
		}{
			{
				name:        "non ok code",
				resp:        response(http.StatusForbidden, ""),
				errContains: "http code: 403",
			},
			{
				name:        "ok code with non empty response",
				resp:        response(http.StatusOK, "Something went wrong"),
				errContains: "server has returned body with 200 ok",
			},
			{
				name:        "json response",
				resp:        response(http.StatusInternalServerError, `{"code":"internal_error","message":"something went wrong"}`),
				errContains: "http code: 500, error: internal_error, message: something went wrong",
			},
			{
				name:        "non-json error",
				resp:        response(http.StatusBadRequest, "invalid request format"),
				errContains: "response isn't json",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				client := pyroscope.NewClient("http://pyroscope.test", "", newTestClient(func(r *http.Request) (*http.Response, error) {
					return tt.resp, nil
				}))

				err := client.Send(context.Background(), payload)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			})
		}
	})

	t.Run("network error on send", func(t *testing.T) {
		client := pyroscope.NewClient("http://pyroscope.test", "", newTestClient(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("dial error")
		}))

		err := client.Send(context.Background(), payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error sending request")
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		client := pyroscope.NewClient("http://pyroscope.test", "", newTestClient(func(r *http.Request) (*http.Response, error) {
			return nil, ctx.Err()
		}))

		err := client.Send(ctx, payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("request body is readable once", func(t *testing.T) {
		var body bytes.Buffer
		client := pyroscope.NewClient("http://pyroscope.test", "", newTestClient(func(r *http.Request) (*http.Response, error) {
			_, err := io.Copy(&body, r.Body)
			require.NoError(t, err)
			return response(http.StatusOK, ""), nil
		}))

		err := client.Send(context.Background(), payload)
		require.NoError(t, err)
		require.NotEmpty(t, body.String())
	})
}
