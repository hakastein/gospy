package pyroscope_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/pyroscope"
)

func newIngestPool(
	statsChannel chan *pyroscope.SendResult,
	roundTrip roundTripFunc,
) *pyroscope.IngestPool {
	client := pyroscope.NewClient("http://pyroscope.test", "", newTestClient(roundTrip))
	return pyroscope.NewIngestPool(
		client,
		pyroscope.NewAppMetadata("test-app", "", 100),
		rate.NewLimiter(rate.Inf, 0),
		1,
		statsChannel,
	)
}

func TestIngestPoolSendsSuccessfulResult(t *testing.T) {
	t.Parallel()

	statsChannel := make(chan *pyroscope.SendResult, 1)
	requests := make(chan struct{}, 1)
	pool := newIngestPool(
		statsChannel,
		func(r *http.Request) (*http.Response, error) {
			requests <- struct{}{}
			return response(http.StatusOK, ""), nil
		},
	)

	input := make(chan *collector.TagCollection, 1)
	input <- collector.NewTagCollection(time.Now(), time.Now(), "env=test", map[string]int{"main;func": 1})
	close(input)

	pool.Start(context.Background(), input)
	pool.Wait()

	require.Len(t, requests, 1)
	result := <-statsChannel
	require.Equal(t, 11, result.Bytes)
	require.NoError(t, result.Err)
}

func TestIngestPoolSendsFailedResult(t *testing.T) {
	t.Parallel()

	sendErr := errors.New("send failed")
	statsChannel := make(chan *pyroscope.SendResult, 1)
	pool := newIngestPool(statsChannel, func(r *http.Request) (*http.Response, error) {
		return nil, sendErr
	})

	input := make(chan *collector.TagCollection, 1)
	input <- collector.NewTagCollection(time.Now(), time.Now(), "env=test", map[string]int{"main;func": 1})
	close(input)

	pool.Start(context.Background(), input)
	pool.Wait()

	result := <-statsChannel
	require.Equal(t, 11, result.Bytes)
	require.ErrorContains(t, result.Err, sendErr.Error())
}

func TestIngestPoolStopsAfterInputClosed(t *testing.T) {
	t.Parallel()

	pool := newIngestPool(
		make(chan *pyroscope.SendResult, 1),
		func(r *http.Request) (*http.Response, error) {
			return response(http.StatusOK, ""), nil
		},
	)

	input := make(chan *collector.TagCollection)
	close(input)

	pool.Start(context.Background(), input)
	pool.Wait()
}

func TestIngestPoolDoesNotBlockOnStatsSendAfterContextCancellation(t *testing.T) {
	t.Parallel()

	statsChannel := make(chan *pyroscope.SendResult)
	requestProcessed := make(chan struct{}, 1)
	pool := newIngestPool(statsChannel, func(r *http.Request) (*http.Response, error) {
		requestProcessed <- struct{}{}
		return response(http.StatusOK, ""), nil
	})

	input := make(chan *collector.TagCollection, 1)
	input <- collector.NewTagCollection(time.Now(), time.Now(), "env=test", map[string]int{"main;func": 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	close(input)

	pool.Start(ctx, input)
	pool.Wait()
}

func TestIngestPoolAllowsNilStatsChannel(t *testing.T) {
	t.Parallel()

	pool := newIngestPool(nil, func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, ""), nil
	})

	input := make(chan *collector.TagCollection, 1)
	input <- collector.NewTagCollection(time.Now(), time.Now(), "env=test", map[string]int{"main;func": 1})
	close(input)

	pool.Start(context.Background(), input)
	pool.Wait()
}
