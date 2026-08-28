package pyroscope

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/collector"
	"golang.org/x/time/rate"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestWorkerStopsAfterInputClosedAndQueueDrained(t *testing.T) {
	client := NewClient("http://pyroscope.test", "", &http.Client{
		Transport: testRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(http.NoBody),
				Header:     make(http.Header),
			}, nil
		}),
	})

	collectorInstance := collector.NewTraceCollector()
	worker := NewWorker(client, NewAppMetadata("test-app", "", 100), collectorInstance, rate.NewLimiter(rate.Inf, 0), make(chan *RequestStats, 1))

	inputClosed := make(chan struct{})
	close(inputClosed)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	worker.Start(ctx, inputClosed)
	worker.Wait()
}
