package main

import (
	"bytes"
	"fmt"
	"go.uber.org/zap"
	"log"
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
	sync.RWMutex
}

func (sc *SampleCollection) makeRequest(name string, buffer bytes.Buffer) *Request {
	return &Request{
		from:       sc.from.Unix(),
		until:      sc.until.Unix(),
		sampleRate: sc.rateHz,
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
	pyroscopeURL string,
	pyroscopeAuth string,
	data bytes.Buffer,
	name string,
	from int64,
	until int64,
	sampleRate int,
) (int, error) {
	client := &http.Client{}

	req, err := http.NewRequest("POST", pyroscopeURL+"/ingest", &data)
	if err != nil {
		return 0, fmt.Errorf("error creating request: %s", err)
	}

	req.Header.Set("Content-Type", "text/plain")
	if pyroscopeAuth != "" {
		req.Header.Set("Authorization", pyroscopeAuth)
	}

	q := req.URL.Query()
	q.Add("name", fmt.Sprintf("%s", name))
	q.Add("from", fmt.Sprintf("%d", from))
	q.Add("until", fmt.Sprintf("%d", until))
	q.Add("sampleRate", fmt.Sprintf("%d", sampleRate))
	q.Add("format", "folded")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("error sending request: %s", err)
	}

	if resp.Body.Close() != nil {
		return 0, fmt.Errorf("got error while closing response body: %s", err)
	}

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("received non-OK response: %s", resp.Status)
	}

	return resp.StatusCode, nil
}

func makeRequest(
	channel chan *Request,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	logger *zap.Logger,
) {
	var bytesSent, queries int
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			if queries > 0 {
				// showing sending info
				logger.Info("data sent",
					zap.Int("queries", queries),
					zap.Int("bytes", bytesSent),
				)
				bytesSent = 0
				queries = 0
			}
		}
	}()

	for req := range channel {
		req.Lock()

		if bytesSent+req.bytes > rateBytes {
			logger.Warn("sending too fast, consider increasing rate limit")
			<-ticker.C // wait for ticker and then continue
		}

		// trying to send sample
		code, err := sendSample(pyroscopeURL, pyroscopeAuth, req.data, req.name, req.from, req.until, req.sampleRate)

		if err != nil {
			logger.Warn("error sending request", zap.Error(err))
		}

		// samples successfully sent
		if code == http.StatusOK {
			bytesSent += req.bytes
			queries++
		}

		if code != http.StatusOK && code != 0 {
			if req.retries < RetryCount {
				req.retries++
				go func(r *Request) {
					channel <- r
				}(req)
			} else {
				logger.Error("failed to send request", zap.Int("retries", req.retries))
			}
		}

		req.Unlock()
	}
}

func readSamples(
	channel chan *SampleCollection,
	requestQueue chan *Request,
	app string,
	staticTags string,
	rateBytes int,
) {
	for sampleCollection := range channel {
		sampleCollection.RLock()

		for dynamicTags, tagSamples := range sampleCollection.samples {
			var buffer bytes.Buffer
			requestSize := 0
			name := fmt.Sprintf("%s{%s}", app, combineTags(staticTags, dynamicTags))

			for _, sample := range tagSamples {
				line := fmt.Sprintf("%s %d\n", sample.sample, sample.count)
				lineSize := len(line)

				// Check if adding the new line would exceed the rateBytes
				if requestSize+lineSize > rateBytes {
					// push request to queue
					requestQueue <- sampleCollection.makeRequest(name, buffer)

					// Reset the buffer and requestSize for the new request
					buffer.Reset()
					requestSize = 0
				}

				// Write the line to the buffer
				buffer.WriteString(line)
				requestSize += lineSize
			}

			// Send any remaining data in the buffer as the final request
			if requestSize > 0 {
				log.Printf("len size %d", requestSize)
				log.Printf("buffer size %d", buffer.Len())
				requestQueue <- sampleCollection.makeRequest(name, buffer)
			}
		}

		sampleCollection.RUnlock()
	}
}

func sendToPyroscope(
	samplesChannel chan *SampleCollection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	logger *zap.Logger,
) {
	requestQueue := make(chan *Request)

	// read requestQueue and send data to pyroscope
	go makeRequest(requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, logger)

	// read samples from samplesChannel and send it to requestQueue
	readSamples(samplesChannel, requestQueue, app, staticTags, rateBytes)
}
