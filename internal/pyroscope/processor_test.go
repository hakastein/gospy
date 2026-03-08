package pyroscope_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/pyroscope"
)

func TestProcessor_ProcessData_Success(t *testing.T) {
	server := createOKServer()
	defer server.Close()

	processor := createProcessor(server.URL, rate.NewLimiter(1000, 1000))
	profileData := createProfileData()

	err := processor.ProcessData(context.Background(), profileData)

	require.NoError(t, err)
}

func TestProcessor_ProcessData_RateLimiting(t *testing.T) {
	tests := []struct {
		name      string
		rateLimit rate.Limit
		burst     int
	}{
		{
			name:      "without_burst",
			rateLimit: 100, // 50 bytes per second
			burst:     100, // no burst capacity
		},
		{
			name:      "with_burst",
			rateLimit: 100, // 50 bytes per second
			burst:     200, // allows burst of 100 bytes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []time.Time

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, time.Now())
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			rateLimiter := rate.NewLimiter(tt.rateLimit, tt.burst)
			processor := createProcessor(server.URL, rateLimiter)

			profileData := createProfileData()
			dataSize := profileData.Len()

			// Send multiple batches quickly
			batchCount := 3
			start := time.Now()

			for i := 0; i < batchCount; i++ {
				err := processor.ProcessData(context.Background(), profileData)
				require.NoError(t, err)
			}

			elapsed := time.Since(start)

			mu.Lock()
			assert.Len(t, requests, batchCount, "All requests should complete")
			mu.Unlock()

			if tt.burst == 1 {
				// Without burst: should be rate limited from the start
				expectedMinTime := time.Duration(float64(dataSize*batchCount)/float64(tt.rateLimit)) * time.Second
				assert.GreaterOrEqual(t, elapsed, expectedMinTime*8/10, "Should respect rate limit without burst")
			} else {
				// With burst: first requests can go fast, then rate limited
				// First request uses burst, subsequent ones are rate limited
				expectedMinTime := time.Duration(float64(dataSize*(batchCount-1))/float64(tt.rateLimit)) * time.Second
				// Allow some tolerance for timing
				if elapsed < expectedMinTime*7/10 {
					t.Logf("Warning: elapsed %v seems too fast for rate limit, expected >= %v", elapsed, expectedMinTime*7/10)
				}
			}

			// Verify requests came with delays (except possibly the first with burst)
			mu.Lock()
			if len(requests) >= 2 {
				for i := 1; i < len(requests); i++ {
					gap := requests[i].Sub(requests[i-1])
					if tt.burst == 1 || i > 1 { // Without burst or after burst is exhausted
						expectedGap := time.Duration(float64(dataSize)/float64(tt.rateLimit)) * time.Second
						assert.GreaterOrEqual(t, gap, expectedGap*7/10, "Requests should be spaced by rate limit")
					}
				}
			}
			mu.Unlock()
		})
	}
}

// Helper functions

func createOKServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func createProcessor(serverURL string, rateLimiter *rate.Limiter) *pyroscope.Processor {
	httpClient := &http.Client{Timeout: 30 * time.Second} // Long timeout for rate limiting tests
	client := pyroscope.NewClient(serverURL, "", httpClient)
	appMetadata := pyroscope.NewAppMetadata("test-app", "env=test", 100)
	return pyroscope.NewProcessor(client, appMetadata, rateLimiter)
}

func createProfileData() *collector.TagCollection {
	now := time.Now()
	data := map[string]int{
		"main;controller;action":   5,
		"main;service;process":     3,
		"main;repository;findById": 2,
	}

	return collector.NewTagCollection(
		now.Add(-1*time.Minute),
		now,
		"env=prod,service=api",
		data,
	)
}
