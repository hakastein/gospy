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

// AppMetadata represents pyroscope application's static information.
type AppMetadata struct {
	appName    string
	staticTags string
	sampleRate int
}

// NewAppMetadata creates a new AppMetadata instance with the given app name, static tags, and sample rate.
func NewAppMetadata(appName, staticTags string, sampleRate int) *AppMetadata {
	return &AppMetadata{
		appName:    appName,
		staticTags: staticTags,
		sampleRate: sampleRate,
	}
}

// Payload represents data to be sent to Pyroscope, including app metadata and profile information.
type Payload struct {
	metadata    *AppMetadata
	profileData TagData
}

// NewPayload creates a new Payload instance with the AppMetadata and profile data.
func (app *AppMetadata) NewPayload(data TagData) Payload {
	return Payload{
		metadata:    app,
		profileData: data,
	}
}

// fullAppName combines the app name with static and dynamic tags in Pyroscope format.
func (app *AppMetadata) fullAppName(dynamicTags string) string {
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

// BodyReader returns an io.Reader that produces the profile data in Pyroscope's folded format.
func (payload *Payload) BodyReader() io.Reader {
	var buffer bytes.Buffer
	first := true
	for sample, count := range payload.profileData.Data() {
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

// QueryString generates the URL query string with all parameters for the Pyroscope API.
func (payload *Payload) QueryString() string {
	var builder strings.Builder

	builder.WriteString("name=")
	builder.WriteString(url.QueryEscape(payload.metadata.fullAppName(payload.profileData.Tags())))
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(payload.profileData.From().Unix(), 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(payload.profileData.Until().Unix(), 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(payload.metadata.sampleRate))
	builder.WriteString("&format=folded")

	return builder.String()
}
