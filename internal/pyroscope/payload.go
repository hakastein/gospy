package pyroscope

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/hakastein/gospy/internal/collector"
)

const (
	appNameEstimatedLength  = 50
	appQueryEstimatedLength = 150
)

type appMetadata struct {
	appName    string
	staticTags string
	sampleRate int
}

type payload struct {
	query string
	body  []byte
}

func newAppMetadata(appName, staticTags string, sampleRate int) *appMetadata {
	return &appMetadata{
		appName:    appName,
		staticTags: staticTags,
		sampleRate: sampleRate,
	}
}

func (app *appMetadata) newPayload(batch *collector.TagCollection) payload {
	return payload{
		query: app.queryString(batch),
		body:  foldedBody(batch),
	}
}

// fullAppName combines the app name with static and dynamic tags in Pyroscope format.
func (app *appMetadata) fullAppName(dynamicTags string) string {
	var builder strings.Builder
	builder.Grow(appNameEstimatedLength)

	builder.WriteString(app.appName)
	builder.WriteRune('{')
	if app.staticTags != "" {
		builder.WriteString(app.staticTags)
	}
	if app.staticTags != "" && dynamicTags != "" {
		builder.WriteRune(',')
	}
	if dynamicTags != "" {
		builder.WriteString(dynamicTags)
	}
	builder.WriteRune('}')

	return builder.String()
}

func (app *appMetadata) queryString(batch *collector.TagCollection) string {
	var builder strings.Builder
	builder.Grow(appQueryEstimatedLength)

	builder.WriteString("name=")
	builder.WriteString(url.QueryEscape(app.fullAppName(batch.Tags())))
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(batch.From().Unix(), 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(batch.Until().Unix(), 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(app.sampleRate))
	builder.WriteString("&format=folded")

	return builder.String()
}

// foldedBody renders the batch as Pyroscope's folded format: one "stack count" line per stack.
func foldedBody(batch *collector.TagCollection) []byte {
	var body []byte

	first := true
	for stack, count := range batch.Data() {
		if first {
			first = false
		} else {
			body = append(body, '\n')
		}
		body = append(body, stack...)
		body = append(body, ' ')
		body = strconv.AppendInt(body, int64(count), 10)
	}

	return body
}
