package pyroscope_test

import (
	"context"
	"io"
	"net/http"
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
	processor := createProcessor(rate.NewLimiter(1000, 1000), func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(http.NoBody),
			Header:     make(http.Header),
		}, nil
	})
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
			rateLimit: 100,
			burst:     100,
		},
		{
			name:      "with_burst",
			rateLimit: 100,
			burst:     200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []time.Time

			rateLimiter := rate.NewLimiter(tt.rateLimit, tt.burst)
			processor := createProcessor(rateLimiter, func(r *http.Request) (*http.Response, error) {
				mu.Lock()
				requests = append(requests, time.Now())
				mu.Unlock()

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(http.NoBody),
					Header:     make(http.Header),
				}, nil
			})

			profileData := createProfileData()
			dataSize := profileData.Len()
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

			delayedBytes := max(0, dataSize*batchCount-tt.burst)
			expectedMinTime := time.Duration(float64(delayedBytes)/float64(tt.rateLimit)) * time.Second
			if elapsed < expectedMinTime*7/10 {
				t.Fatalf("elapsed %v is below expected minimum %v for rate %v burst %d", elapsed, expectedMinTime*7/10, tt.rateLimit, tt.burst)
			}
		})
	}
}

func createProcessor(rateLimiter *rate.Limiter, transport roundTripFunc) *pyroscope.Processor {
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}
	client := pyroscope.NewClient("http://pyroscope.test", "", httpClient)
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
