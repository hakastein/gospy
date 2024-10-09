package pyroscope

import (
	"bytes"
	"context"
	"fmt"
	"gospy/internal/sample"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
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
) {
	var (
		bytesSent     int
		queries       int
		responseCode  int
		responseError error
		ticker        = time.NewTicker(time.Second)
		client        = &http.Client{
			Timeout: 10 * time.Second,
		}
	)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-requestQueue:
			if !ok {
				return
			}
			if bytesSent+req.bytes > rateBytes {
				log.Warn().Msg("sending too fast, consider increasing rate limit")
				// Wait for rate limit reset
				<-ticker.C
				continue
			}

			responseCode, responseError = sendSample(ctx, client, pyroscopeURL, pyroscopeAuth, &req.data, req.name, req.from, req.until, req.sampleRate)
			if responseError != nil {
				log.Warn().Err(responseError).Msg("error sending request")
			}

			if responseCode == http.StatusOK {
				bytesSent += req.bytes
				log.Trace().Str("name", req.name).Msg("sent request for name")
				queries++
			} else {
				if req.retries < 2 {
					req.retries++
					// Retry the request
					go func() {
						requestQueue <- req
					}()
				} else {
					log.Warn().Int("retries", req.retries).Msg("failed to send request after retries")
				}
			}
		case <-ticker.C:
			if queries > 0 {
				log.Info().Int("queries", queries).Int("bytes", bytesSent).Msg("data sent")
				bytesSent = 0
				queries = 0
			}
		}
	}
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

	go sendRequest(ctx, requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes)

	readSamples(ctx, samplesChannel, requestQueue, app, staticTags, rateBytes)

}
