package pyroscope

import (
	"bytes"
	"context"
	"fmt"
	"gospy/internal/sample"
	"net/http"
	"time"

	"go.uber.org/zap"
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

func sendRequest(
	ctx context.Context,
	requestQueue chan *Request,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	logger *zap.Logger,
) {
	var bytesSent, queries int
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-requestQueue:
			if !ok {
				return
			}
			if bytesSent+req.bytes > rateBytes {
				logger.Warn("sending too fast, consider increasing rate limit")
				// Wait for rate limit reset
				<-ticker.C
				continue
			}

			code, err := sendSample(ctx, client, pyroscopeURL, pyroscopeAuth, &req.data, req.name, req.from, req.until, req.sampleRate)
			if err != nil {
				logger.Warn("error sending request", zap.Error(err))
			}

			if code == http.StatusOK {
				bytesSent += req.bytes
				queries++
			} else {
				if req.retries < 2 {
					req.retries++
					// Retry the request
					go func() {
						requestQueue <- req
					}()
				} else {
					logger.Warn("failed to send request after retries", zap.Int("retries", req.retries))
				}
			}
		case <-ticker.C:
			if queries > 0 {
				logger.Info("data sent", zap.Int("queries", queries), zap.Int("bytes", bytesSent))
				bytesSent = 0
				queries = 0
			}
		}
	}
}

func readSamples(
	ctx context.Context,
	samplesChannel <-chan *sample.Collection,
	requestQueue chan *Request,
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
				var buffer bytes.Buffer
				requestSize := 0
				name := fmt.Sprintf("%s{%s}", app, combineTags(staticTags, dynamicTags))

				for _, smpl := range tagSamples {
					line := smpl.String()
					lineSize := len(line)

					if requestSize+lineSize > rateBytes {
						req := makeRequest(sampleCollection, name, buffer)
						// Enqueue the request
						select {
						case requestQueue <- req:
							// Successfully enqueued request
						case <-ctx.Done():
							return
						}
						buffer.Reset()
						requestSize = 0
					}

					buffer.WriteString(line)
					buffer.WriteString("\n")
					requestSize += lineSize
				}

				if requestSize > 0 {
					req := makeRequest(sampleCollection, name, buffer)
					// Enqueue the request
					select {
					case requestQueue <- req:
						// Successfully enqueued request
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}
}

func recoverAndLogPanic(logger *zap.Logger, message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		if err, ok := r.(error); ok {
			logger.Error(message, zap.Error(err))
		} else {
			logger.Error(message, zap.Any("error", r))
		}
		cancel()
	}
}

func SendToPyroscope(
	ctx context.Context,
	logger *zap.Logger,
	cancel context.CancelFunc,
	samplesChannel <-chan *sample.Collection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
) {
	defer recoverAndLogPanic(logger, "panic recovered in sendToPyroscope", cancel)

	requestQueue := make(chan *Request)

	go sendRequest(ctx, requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, logger)

	readSamples(ctx, samplesChannel, requestQueue, app, staticTags, rateBytes)

	// Close the requestQueue after readSamples returns
	close(requestQueue)
}
