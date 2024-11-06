package pyroscope

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
	"gospy/internal/sample"
	"net/http"
	"sync"
	"time"
)

type stats struct {
	queries int
	bytes   int
	blocked int
}

func processRequests(
	ctx context.Context,
	requestQueue <-chan *requestData,
	client *http.Client,
	limiter *rate.Limiter,
	pyroscopeURL string,
	pyroscopeAuth string,
	statsChannel chan<- stats,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-requestQueue:
			if !ok {
				return
			}

			stats := stats{}

			if !limiter.AllowN(time.Now(), req.bytes) {
				stats.blocked = 1
			}

			if err := limiter.WaitN(ctx, req.bytes); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				log.Warn().Err(err).Msg("limiter error")
				continue
			}

			maxRetries := 2
			backoff := 100 * time.Millisecond

			for attempt := 0; attempt <= maxRetries; attempt++ {
				err := req.send(ctx, client, pyroscopeURL, pyroscopeAuth)
				if err == nil {
					stats.queries = 1
					stats.bytes = req.bytes
					log.Debug().Str("name", req.name).Msg("request sent successfully")
					break
				}

				if attempt < maxRetries {
					log.Warn().
						Err(err).
						Int("attempt", attempt+1).
						Msg("retrying request")
					time.Sleep(backoff)
					backoff *= 2
				} else {
					log.Error().
						Err(err).
						Msg("failed to send request after retries")
				}
			}

			select {
			case statsChannel <- stats:
			case <-ctx.Done():
				return
			}
		}
	}
}

func sendRequests(
	ctx context.Context,
	requestQueue chan *requestData,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	workerCount int,
) {
	limiter := rate.NewLimiter(rate.Limit(rateBytes), rateBytes)
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	statsChannel := make(chan stats)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processRequests(ctx, requestQueue, httpClient, limiter, pyroscopeURL, pyroscopeAuth, statsChannel)
		}()
	}

	go func() {
		logStats(ctx, statsChannel)
	}()

	wg.Wait()
	close(statsChannel)
}

func logStats(ctx context.Context, statsChannel <-chan stats) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var totalQueries, totalBytes, totalBlocked int
	const limiterWarningThreshold = 10

	for {
		select {
		case <-ctx.Done():
			return
		case stat, ok := <-statsChannel:
			if !ok {
				return
			}
			totalQueries += stat.queries
			totalBytes += stat.bytes
			totalBlocked += stat.blocked
		case <-ticker.C:
			if totalQueries > 0 || totalBytes > 0 {
				log.Info().
					Int("queries", totalQueries).
					Int("bytes", totalBytes).
					Msg("data sent")
				totalQueries = 0
				totalBytes = 0
			}
			if totalBlocked > limiterWarningThreshold {
				log.Warn().
					Int("block_count", totalBlocked).
					Msg("Sender is frequently hitting the bandwidth limit; consider increasing the rate limit")
			}
			totalBlocked = 0
		}
	}
}

func processTagSamples(
	sampleCollection *sample.Collection,
	appName string,
	tagSamples map[uint64]*sample.Sample,
	requestQueue chan<- *requestData,
	rateBytes int,
) {
	var (
		currentBuffer = bytes.NewBuffer(nil)
		requestSize   int
	)

	for _, smpl := range tagSamples {
		line := smpl.String()
		lineSize := len(line) + 1 // +1 for the newline character

		if requestSize+lineSize > rateBytes {
			// split buffer into multiples to avoid extra large queries that bigger then rate limit itself
			requestQueue <- newRequest(sampleCollection, appName, *currentBuffer)
			currentBuffer = bytes.NewBuffer(nil) // Create a new buffer
			requestSize = 0
		}

		currentBuffer.WriteString(line)
		currentBuffer.WriteString("\n")
		requestSize += lineSize
	}

	if requestSize > 0 {
		// Send any remaining data in the buffer
		requestQueue <- newRequest(sampleCollection, appName, *currentBuffer)
	}
}

func SendToPyroscope(
	ctx context.Context,
	samplesChannel <-chan *sample.Collection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateHz int,
	rateBytes int,
) {
	requestQueue := make(chan *requestData, 100)
	defer close(requestQueue)

	go sendRequests(ctx, requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, 5)

	for {
		select {
		case <-ctx.Done():
			return
		case sampleCollection, ok := <-samplesChannel:
			if !ok {
				return
			}

			for dynamicTags, tagSamples := range sampleCollection.Samples() {
				appName := fmt.Sprintf("%s{%s}", app, combineTags(staticTags, dynamicTags))
				processTagSamples(sampleCollection, appName, tagSamples, requestQueue, rateBytes)
			}
		}
	}
}
