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
}

func (req *Request) send(
	ctx context.Context,
	client *http.Client,
	pyroscopeURL string,
	pyroscopeAuth string,
) error {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", pyroscopeURL+"/ingest", &req.data)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "text/plain")
	if pyroscopeAuth != "" {
		httpReq.Header.Set("Authorization", pyroscopeAuth)
	}

	q := httpReq.URL.Query()
	q.Add("name", req.name)
	q.Add("from", fmt.Sprintf("%d", req.from))
	q.Add("until", fmt.Sprintf("%d", req.until))
	q.Add("sampleRate", fmt.Sprintf("%d", req.sampleRate))
	q.Add("format", "folded")
	httpReq.URL.RawQuery = q.Encode()

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received unexpected response code: %s", resp.Status)
	}

	return nil
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

type Stats struct {
	Queries int
	Bytes   int
	Blocked int
}

func processRequests(
	ctx context.Context,
	requestQueue <-chan *Request,
	client *http.Client,
	limiter *rate.Limiter,
	pyroscopeURL string,
	pyroscopeAuth string,
	statsChannel chan<- Stats,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-requestQueue:
			if !ok {
				return
			}

			stats := Stats{}

			if !limiter.AllowN(time.Now(), req.bytes) {
				stats.Blocked = 1
			}

			if err := limiter.WaitN(ctx, req.bytes); err != nil {
				if err == context.Canceled || err == context.DeadlineExceeded {
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
					stats.Queries = 1
					stats.Bytes = req.bytes
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

	statsChannel := make(chan Stats)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			processRequests(ctx, requestQueue, client, limiter, pyroscopeURL, pyroscopeAuth, statsChannel)
		}()
	}

	go func() {
		logStats(ctx, statsChannel)
	}()

	wg.Wait()
	close(statsChannel)
}

func logStats(ctx context.Context, statsChannel <-chan Stats) {
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
			totalQueries += stat.Queries
			totalBytes += stat.Bytes
			totalBlocked += stat.Blocked
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

func readSamples(
	ctx context.Context,
	samplesChannel <-chan *sample.Collection,
	requestQueue chan<- *Request,
	app string,
	staticTags string,
	rateBytes int,
) {
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

func processTagSamples(
	sampleCollection *sample.Collection,
	appName string,
	tagSamples map[uint64]*sample.Sample,
	requestQueue chan<- *Request,
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
			requestQueue <- makeRequest(sampleCollection, appName, *currentBuffer)
			currentBuffer = bytes.NewBuffer(nil) // Create a new buffer
			requestSize = 0
		}

		currentBuffer.WriteString(line)
		currentBuffer.WriteString("\n")
		requestSize += lineSize
	}

	if requestSize > 0 {
		// Send any remaining data in the buffer
		requestQueue <- makeRequest(sampleCollection, appName, *currentBuffer)
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

	go sendRequests(ctx, requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, 5)

	readSamples(ctx, samplesChannel, requestQueue, app, staticTags, rateBytes)
}
