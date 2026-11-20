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

	// invalidUTF8Replacement stands in for bytes that are not UTF-8: Pyroscope rejects the
	// whole batch over a single such byte in a label or a frame.
	invalidUTF8Replacement = "\uFFFD"
)

type appMetadata struct {
	appName string
}

type payload struct {
	query string
	body  []byte
}

func newAppMetadata(appName string) *appMetadata {
	return &appMetadata{appName: appName}
}

func (app *appMetadata) newPayload(batch *collector.TagCollection) payload {
	return payload{
		query: app.queryString(batch),
		body:  foldedBody(batch),
	}
}

// fullAppName combines the app name with the batch's tag set in Pyroscope format; every tag
// of a sample, static or dynamic, is already in that set.
func (app *appMetadata) fullAppName(tags string) string {
	var builder strings.Builder
	builder.Grow(appNameEstimatedLength)

	builder.WriteString(app.appName)
	builder.WriteRune('{')
	builder.WriteString(tags)
	builder.WriteRune('}')

	return builder.String()
}

func (app *appMetadata) queryString(batch *collector.TagCollection) string {
	var builder strings.Builder
	builder.Grow(appQueryEstimatedLength)

	builder.WriteString("name=")
	builder.WriteString(url.QueryEscape(strings.ToValidUTF8(app.fullAppName(batch.Tags()), invalidUTF8Replacement)))
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(batch.From().Unix(), 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(batch.Until().Unix(), 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(batch.SampleRate()))
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
		body = append(body, strings.ToValidUTF8(stack, invalidUTF8Replacement)...)
		body = append(body, ' ')
		body = strconv.AppendInt(body, int64(count), 10)
	}

	return body
}
