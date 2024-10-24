package pyroscope

import (
	"bytes"
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
	"gospy/internal/sample"
	"net/http"
	"sync"
	"time"
)

type Request struct {
	data       bytes.Buffer
	name       string
	from       int64
	until      int64
	sampleRate int
	bytes      int
	retries    int
}

func makeRequest(sc *sample.Collection, name string, buffer bytes.Buffer) *Request {
	from, until, rateHz := sc.Props()
	return &Request{
		from:       from,
		until:      until,
		sampleRate: rateHz,
		data:       buffer,
		name:       name,
		bytes:      buffer.Len(),
	}
}

func combineTags(staticTags, dynamicTags string) string {
	if dynamicTags == "" {
		return staticTags
	}
	if staticTags == "" {
		return dynamicTags
	}
	return staticTags + "," + dynamicTags
}

func sendSample(
	ctx context.Context,
	client *http.Client,
	pyroscopeURL string,
	pyroscopeAuth string,
	data *bytes.Buffer,
	name string,
	from int64,
	until int64,
	sampleRate int,
) (int, error) {
	req, reqError := http.NewRequestWithContext(ctx, "POST", pyroscopeURL+"/ingest", data)
	if reqError != nil {
		return 0, fmt.Errorf("error creating request: %w", reqError)
	}

	req.Header.Set("Content-Type", "text/plain")
	if pyroscopeAuth != "" {
		req.Header.Set("Authorization", pyroscopeAuth)
	}

	q := req.URL.Query()
	q.Add("name", name)
	q.Add("from", fmt.Sprintf("%d", from))
	q.Add("until", fmt.Sprintf("%d", until))
	q.Add("sampleRate", fmt.Sprintf("%d", sampleRate))
	q.Add("format", "folded")
	req.URL.RawQuery = q.Encode()

	resp, reqError := client.Do(req)
	if reqError != nil {
		return 0, fmt.Errorf("error sending request: %w", reqError)
	}

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("received unexpected response code: %s", resp.Status)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		return 0, closeErr
	}

	return resp.StatusCode, nil
}

// sendRequest read request from requestQueue and sends it to pyroscope
func sendRequest(
	ctx context.Context,
	requestQueue chan *Request,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	workerCount int,
) {
	limiter := rate.NewLimiter(rate.Limit(rateBytes), rateBytes)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(workerCount)

	var (
		totalQueries      int
		totalBytes        int
		countsMutex       sync.Mutex
		limiterBlockCount int
		limiterMutex      sync.Mutex
	)

	// Start worker goroutines
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-requestQueue:
					if !ok {
						return
					}

					// Check if tokens are immediately available
					if !limiter.AllowN(time.Now(), req.bytes) {
						// Limiter would block; increment the block counter
						limiterMutex.Lock()
						limiterBlockCount++
						limiterMutex.Unlock()
					}

					// Wait for tokens
					if err := limiter.WaitN(ctx, req.bytes); err != nil {
						if ctx.Err() != nil {
							return
						}
						log.Warn().Err(err).Msg("limiter error")
						continue
					}

					maxRetries := 2
					backoff := 100 * time.Millisecond

					for attempt := 0; attempt <= maxRetries; attempt++ {
						responseCode, responseError := sendSample(
							ctx,
							client,
							pyroscopeURL,
							pyroscopeAuth,
							&req.data,
							req.name,
							req.from,
							req.until,
							req.sampleRate,
						)
						if responseError == nil && responseCode == http.StatusOK {

							countsMutex.Lock()
							totalQueries++
							totalBytes += req.bytes
							countsMutex.Unlock()

							log.Debug().Str("name", req.name).Msg("request sent successfully")
							break
						}

						if attempt < maxRetries {
							log.Warn().
								Err(responseError).
								Int("attempt", attempt+1).
								Msg("retrying request")
							time.Sleep(backoff)
							backoff *= 2
						} else {
							log.Error().
								Err(responseError).
								Msg("failed to send request after retries")
						}
					}
				}
			}
		}()
	}

	// Start logging goroutine
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		// Threshold for logging the warning (e.g., if blocked more than 10 times per second)
		const limiterWarningThreshold = 10

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				countsMutex.Lock()
				if totalQueries > 0 || totalBytes > 0 {
					log.Info().
						Int("queries", totalQueries).
						Int("bytes", totalBytes).
						Msg("data sent")
				}
				totalQueries = 0
				totalBytes = 0
				countsMutex.Unlock()

				// Check if the limiter has blocked frequently
				limiterMutex.Lock()
				blockCount := limiterBlockCount
				limiterBlockCount = 0 // Reset the counter
				limiterMutex.Unlock()

				if blockCount > limiterWarningThreshold {
					log.Warn().
						Int("block_count", blockCount).
						Msg("Sender is frequently hitting the bandwidth limit; consider increasing the rate limit")
				}
			}
		}
	}()

	wg.Wait()
}

// readSamples makes request object from samplesChannel and put them in requestQueue channel
func readSamples(
	ctx context.Context,
	samplesChannel <-chan *sample.Collection,
	requestQueue chan *Request,
	app string,
	staticTags string,
	rateBytes int,
) {
	var (
		appName     string
		line        string
		lineSize    int
		requestSize int
	)

	for {
		select {
		case <-ctx.Done():
			return
		case sampleCollection, ok := <-samplesChannel:
			if !ok {
				return
			}

			for dynamicTags, tagSamples := range sampleCollection.Samples() {
				var buffer bytes.Buffer
				requestSize = 0
				appName = fmt.Sprintf("%s{%s}", app, combineTags(staticTags, dynamicTags))
				for _, smpl := range tagSamples {
					line = smpl.String()
					lineSize = len(line)

					// if is too much samples to send in one request split them
					if requestSize+lineSize > rateBytes {
						requestQueue <- makeRequest(sampleCollection, appName, buffer)
						buffer.Reset()
						requestSize = 0
					}

					buffer.WriteString(line)
					buffer.WriteString("\n")
					requestSize += lineSize
				}

				if requestSize > 0 {
					requestQueue <- makeRequest(sampleCollection, appName, buffer)
				}
			}
		}
	}
}

func SendToPyroscope(
	ctx context.Context,
	samplesChannel <-chan *sample.Collection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
) {
	requestQueue := make(chan *Request, 100)
	defer close(requestQueue)

	go sendRequest(ctx, requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, 5)

	readSamples(ctx, samplesChannel, requestQueue, app, staticTags, rateBytes)

}
