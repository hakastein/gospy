package pyroscope

import (
	"bytes"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type TagData interface {
	Tags() string
	From() time.Time
	Until() time.Time
	Data() map[string]int
}

// AppData represents pyroscope app static information
type AppData struct {
	appName    string
	staticTags string
	sampleRate int
}

func NewAppData(appName, staticTags string, sampleRate int) *AppData {
	return &AppData{
		appName:    appName,
		staticTags: staticTags,
		sampleRate: sampleRate,
	}
}

type IngestData struct {
	app     *AppData
	tagData TagData
}

func (app *AppData) IngestData(data TagData) IngestData {
	return IngestData{
		app:     app,
		tagData: data,
	}
}

func (app *AppData) getAppName(dynamicTags string) string {
	var builder strings.Builder

	builder.WriteString(app.appName)
	builder.WriteString("{")
	if app.staticTags != "" {
		builder.WriteString(app.staticTags)
		builder.WriteString(",")
	}
	if dynamicTags != "" {
		builder.WriteString(dynamicTags)
	}
	builder.WriteString("}")

	return builder.String()
}

func (id *IngestData) getBody() io.Reader {
	var buffer bytes.Buffer
	first := true
	for sample, count := range id.tagData.Data() {
		if !first {
			buffer.WriteByte('\n')
		} else {
			first = false
		}
		buffer.WriteString(sample)
		buffer.WriteByte(' ')
		buffer.WriteString(strconv.Itoa(count))
	}
	return &buffer
}

func (id *IngestData) MakeQuery() string {
	var builder strings.Builder

	builder.WriteString("name=")
	builder.WriteString(url.QueryEscape(id.app.getAppName(id.tagData.Tags())))
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(id.tagData.From().Unix(), 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(id.tagData.Until().Unix(), 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(id.app.sampleRate))
	builder.WriteString("&format=folded")

	return builder.String()
}
