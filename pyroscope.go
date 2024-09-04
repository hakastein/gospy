package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func toTagString(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}

	first := true

	var sb strings.Builder
	for key, value := range tags {
		if !first {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%s=%s", key, value))
		first = false
	}

	return sb.String()
}

func sendToPyroscope(
	channel chan SampleCollection,
	app string,
	staticTags map[string]string,
	pyroscopeUrl string,
	pyroscopeAuth string,
) {
	log.Print("send to pyroscope")
	client := &http.Client{}

	for {
		val, ok := <-channel
		if ok {
			// Prepare the profiling data in the folded format
			var buffer bytes.Buffer
			for _, sample := range val.samples {
				line := fmt.Sprintf("%.30s %d\n", sample.sample, sample.count)
				fmt.Println(line)
				buffer.WriteString(line)
			}

			// Prepare the request
			req, err := http.NewRequest("POST", pyroscopeUrl+"/ingest", &buffer)
			if err != nil {
				log.Printf("Error creating request: %v", err)
				continue
			}

			// Add headers
			req.Header.Set("Content-Type", "text/plain")

			if pyroscopeAuth != "" {
				req.Header.Set("Authorization", pyroscopeAuth)
			}

			// Add query parameters
			q := req.URL.Query()
			q.Add("name", fmt.Sprintf("%s{%s}", app, toTagString(staticTags)))
			q.Add("from", fmt.Sprintf("%d", val.from.Unix()))
			q.Add("until", fmt.Sprintf("%d", val.to.Unix()))
			q.Add("format", "folded")
			req.URL.RawQuery = q.Encode()

			// Send the request
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
		} else {
			break // exit break loop
		}
	}
}
