package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
)

func combineTags(staticTags, dynamicTags string) string {
	if dynamicTags == "" {
		return staticTags
	}
	if staticTags == "" {
		return dynamicTags
	}
	return staticTags + "," + dynamicTags
}

func sendToPyroscope(
	channel chan *SampleCollection,
	app string,
	staticTags string,
	pyroscopeURL string,
	pyroscopeAuth string,
) {
	log.Print("Sending data to Pyroscope...")
	client := &http.Client{}

	for {
		val, ok := <-channel
		if !ok {
			break
		}

		val.RLock()
		defer val.RUnlock()

		var buffer bytes.Buffer
		for dynamicTags, tagSamples := range val.samples {

			fullTags := combineTags(staticTags, dynamicTags)

			for _, sample := range tagSamples {
				line := fmt.Sprintf("%s %d\n", sample.sample, sample.count)
				buffer.WriteString(line)
			}

			req, err := http.NewRequest("POST", pyroscopeURL+"/ingest", &buffer)
			if err != nil {
				log.Printf("Error creating request: %v", err)
				continue
			}

			req.Header.Set("Content-Type", "text/plain")
			if pyroscopeAuth != "" {
				req.Header.Set("Authorization", pyroscopeAuth)
			}

			q := req.URL.Query()
			q.Add("name", fmt.Sprintf("%s{%s}", app, fullTags))
			q.Add("from", fmt.Sprintf("%d", val.from.Unix()))
			q.Add("until", fmt.Sprintf("%d", val.to.Unix()))
			q.Add("sampleRate", fmt.Sprintf("%d", val.rateHz))
			q.Add("format", "folded")
			req.URL.RawQuery = q.Encode()

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Error sending request: %v", err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Received non-OK response: %s", resp.Status)
			} else {
				log.Print("Data successfully sent to Pyroscope")
			}
		}
	}
}
