package pyroscope

import (
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
)

func TestAppMetadata_fullAppName(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		staticTags  string
		dynamicTags string
		expected    string
	}{
		{
			name:     "no tags",
			appName:  "myapp",
			expected: "myapp{}",
		},
		{
			name:       "only static tags",
			appName:    "myapp",
			staticTags: "env=prod",
			expected:   "myapp{env=prod}",
		},
		{
			name:        "only dynamic tags",
			appName:     "myapp",
			dynamicTags: "user=admin",
			expected:    "myapp{user=admin}",
		},
		{
			name:        "both static and dynamic tags",
			appName:     "myapp",
			staticTags:  "env=prod",
			dynamicTags: "user=admin",
			expected:    "myapp{env=prod,user=admin}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := NewAppMetadata(tt.appName, tt.staticTags, 100)
			result := meta.fullAppName(tt.dynamicTags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPayload_QueryString(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	until := now.Add(10 * time.Second)

	tagData := collector.NewTagCollection(
		now,
		until,
		"region=us-west",
		map[string]int{"main;foo": 42},
	)

	meta := NewAppMetadata("myapp", "env=prod", 100)
	payload := meta.NewPayload(tagData)

	expectedName := url.QueryEscape("myapp{env=prod,region=us-west}")
	expectedQuery := "name=" + expectedName +
		"&from=" + strconv.FormatInt(now.Unix(), 10) +
		"&until=" + strconv.FormatInt(until.Unix(), 10) +
		"&sampleRate=100&format=folded"

	assert.Equal(t, expectedQuery, payload.QueryString())
}

func TestPayload_BodyReader(t *testing.T) {
	tagData := collector.NewTagCollection(
		time.Time{},
		time.Time{},
		"",
		map[string]int{
			"main;foo": 42,
			"main;bar": 21,
		},
	)

	meta := NewAppMetadata("myapp", "", 100)
	payload := meta.NewPayload(tagData)

	reader := payload.BodyReader()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	expectedLines := []string{
		"main;foo 42",
		"main;bar 21",
	}
	assert.ElementsMatch(t, expectedLines, lines)
}

func BenchmarkQueryString(b *testing.B) {
	now := time.Now().Truncate(time.Second)
	until := now.Add(10 * time.Second)

	tagData := collector.NewTagCollection(
		now,
		until,
		"region=us-west",
		map[string]int{"main;foo": 42},
	)

	meta := NewAppMetadata("myapp", "env=prod", 100)
	payload := meta.NewPayload(tagData)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = payload.QueryString()
	}
}

func BenchmarkBodyReader(b *testing.B) {
	data := make(map[string]int)
	for i := 0; i < 100; i++ {
		data["main;foo;bar;baz;"+strconv.Itoa(i)] = i * 10
	}

	now := time.Now().Truncate(time.Second)
	until := now.Add(10 * time.Second)

	tagData := collector.NewTagCollection(
		now,
		until,
		"region=us-west",
		data,
	)

	meta := NewAppMetadata("myapp", "env=prod", 100)
	payload := meta.NewPayload(tagData)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reader := payload.BodyReader()
		_, _ = io.ReadAll(reader)
	}
}
