package main

import (
	"bytes"
	"fmt"
	"go.uber.org/zap"
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
	var bytesSent int
	var queries int
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for req := range channel {
		req.Lock()

		select {
		case <-ticker.C:
			if queries > 0 {
				logger.Info("data sent",
					zap.Int("queries", queries),
					zap.Int("bytes", bytesSent),
				)
				bytesSent = 0
				queries = 0
			}
		default:
			if bytesSent+req.bytes > rateBytes {
				logger.Warn("sending too fast, consider rising rate-mb parameter in go spy or ingestion_rate_mb in pyroscope")
				<-ticker.C
			}
		}

		code, err := sendSample(pyroscopeURL, pyroscopeAuth, req.data, req.name, req.from, req.until, req.sampleRate)

		if code == http.StatusOK {
			bytesSent += req.bytes
			queries++
		}

		if err != nil {
			logger.Warn("got error while sending request", zap.Error(err))
		}

		if code != http.StatusOK && code != 0 {
			if req.retries < 2 {
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

func sendToPyroscope(
	channel chan *SampleCollection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
	rateBytes int,
	logger *zap.Logger,
) {
	requestQueue := make(chan *Request)

	go makeRequest(requestQueue, pyroscopeURL, pyroscopeAuth, rateBytes, logger)

	for val := range channel {
		val.RLock()

		for dynamicTags, tagSamples := range val.samples {
			var buffer bytes.Buffer

			fullTags := combineTags(staticTags, dynamicTags)

			for _, sample := range tagSamples {
				line := fmt.Sprintf("%s %d\n", sample.sample, sample.count)
				buffer.WriteString(line)
			}

			buffLen := buffer.Len()

			requestQueue <- &Request{
				data:       buffer,
				name:       fmt.Sprintf("%s{%s}", app, fullTags),
				from:       val.from.Unix(),
				until:      val.until.Unix(),
				sampleRate: val.rateHz,
				bytes:      buffLen,
			}

			buffer.Reset()
		}

		val.RUnlock()
	}
}
