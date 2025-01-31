package collector_test

import (
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCollector() *collector.TraceCollector {
	return collector.NewTraceCollector()
}

type sample struct {
	tag   string
	stack string
	time  time.Time
}

type expectedResult struct {
	count     map[string]int
	startTime time.Time
	endTime   time.Time
}

func addSamples(c *collector.TraceCollector, samples []sample) {
	for _, s := range samples {
		c.AddSample(&types.Sample{
			Tags:  s.tag,
			Trace: s.stack,
			Time:  s.time,
		})
	}
}

func collectTags(c *collector.TraceCollector, count int) map[string]*collector.TagCollection {
	result := make(map[string]*collector.TagCollection)
	for i := 0; i < count; i++ {
		tag, ok := c.ConsumeTag()
		if !ok {
			break
		}
		result[tag.Tags] = tag
	}
	return result
}

func verifyCollectedData(t *testing.T, collected map[string]*collector.TagCollection, expected map[string]expectedResult) {
	t.Helper()

	require.Len(t, collected, len(expected), "Number of tags mismatch")

	for tag, exp := range expected {
		actual, exists := collected[tag]
		require.True(t, exists, "missing tag: %s", tag)

		assert.Equal(t, exp.startTime, actual.From, "invalid start time for tag: %s", tag)
		assert.Equal(t, exp.endTime, actual.Until, "invalid end time for tag: %s", tag)
		assert.Equal(t, exp.count, actual.Data, "invalid stack counts for tag: %s", tag)
	}
}

func verifyState(t *testing.T, c *collector.TraceCollector, expected map[string]expectedResult) {
	t.Helper()
	collected := collectTags(c, len(expected))
	verifyCollectedData(t, collected, expected)
}

func verifyOrder(t *testing.T, c *collector.TraceCollector, expectedOrder []string) {
	t.Helper()

	var actualOrder []string
	for _, expectedTag := range expectedOrder {
		tag, ok := c.ConsumeTag()
		require.True(t, ok, "Missing tag %s", expectedTag)
		actualOrder = append(actualOrder, tag.Tags)
	}

	assert.Equal(t, expectedOrder, actualOrder, "Unexpected tag order")
}

func TestTraceCollector(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		c := newTestCollector()
		_, ok := c.ConsumeTag()
		assert.False(t, ok)
	})

	t.Run("MaintainsLRUOrderOnAccess", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []sample{
			{"auth", "main;login", baseTime},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
			{"auth", "main;login", baseTime.Add(10 * time.Millisecond)},
			{"web", "http;handler", baseTime.Add(10 * time.Millisecond)},
		})

		verifyOrder(t, c, []string{"auth", "api"})

		addSamples(c, []sample{
			{"auth", "main;login", baseTime.Add(20 * time.Millisecond)},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
		})

		verifyOrder(t, c, []string{"web", "auth", "api"})
	})

	t.Run("AccumulatesCorrectTimeRanges", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []sample{
			{"auth", "main;login", baseTime},
			{"auth", "main;login", baseTime.Add(10 * time.Millisecond)},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
		})

		verifyState(t, c, map[string]expectedResult{
			"auth": {
				count:     map[string]int{"main;login": 2},
				startTime: baseTime,
				endTime:   baseTime.Add(10 * time.Millisecond),
			},
		})

		addSamples(c, []sample{
			{"auth", "main;logout", baseTime.Add(20 * time.Millisecond)},
			{"auth", "main;login", baseTime.Add(30 * time.Millisecond)},
			{"api", "http;handler", baseTime.Add(40 * time.Millisecond)},
		})

		verifyState(t, c, map[string]expectedResult{
			"auth": {
				count:     map[string]int{"main;login": 1, "main;logout": 1},
				startTime: baseTime.Add(20 * time.Millisecond),
				endTime:   baseTime.Add(30 * time.Millisecond),
			},
			"api": {
				count:     map[string]int{"http;handler": 2},
				startTime: baseTime.Add(20 * time.Millisecond),
				endTime:   baseTime.Add(40 * time.Millisecond),
			},
		})
	})
}
