package pyroscope

import (
	"bytes"
	"strconv"
	"strings"
)

type requestData struct {
	data   bytes.Buffer
	name   string
	from   int64
	until  int64
	rateHz int
}

func (rd *requestData) String() string {
	var builder strings.Builder

	builder.WriteString("name=")
	builder.WriteString(rd.name)
	builder.WriteString("&from=")
	builder.WriteString(strconv.FormatInt(rd.from, 10))
	builder.WriteString("&until=")
	builder.WriteString(strconv.FormatInt(rd.until, 10))
	builder.WriteString("&sampleRate=")
	builder.WriteString(strconv.Itoa(rd.rateHz))
	builder.WriteString("&format=folded")

	return builder.String()
}

func newRequest(buffer bytes.Buffer, name string, from int64, until int64, rateHz int) *requestData {
	return &requestData{
		from:   from,
		until:  until,
		data:   buffer,
		name:   name,
		rateHz: rateHz,
	}
}
