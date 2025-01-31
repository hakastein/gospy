package collector_test

import (
	"strconv"
	"sync"
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
		if ok == false {
			break
		}
		result[tag.Tags] = tag
	}

	return result
}

func verifyState(t *testing.T, c *collector.TraceCollector, expected map[string]expectedResult) {
	t.Helper()

	result := collectTags(c, len(expected))
	require.Len(t, result, len(expected))

	for tag, exp := range expected {
		actual, exists := result[tag]
		require.True(t, exists, "missing tag: %s", tag)

		assert.Equal(t, exp.startTime, actual.From, "invalid start time for tag: %s", tag)
		assert.Equal(t, exp.endTime, actual.Until, "invalid end time for tag: %s", tag)
		assert.Equal(t, exp.count, actual.Data, "invalid stack counts for tag: %s", tag)
	}
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

	t.Run("ReadOrder", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []sample{
			{"auth", "main;login", baseTime},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
			{"auth", "main;login", baseTime.Add(10 * time.Millisecond)},
			{"web", "http;handler", baseTime.Add(10 * time.Millisecond)},
		})

		// extract only 2 of 3 tags and check their order
		verifyOrder(t, c, []string{"auth", "api"})

		// re add extracted
		addSamples(c, []sample{
			{"auth", "main;login", baseTime.Add(20 * time.Millisecond)},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
		})

		verifyOrder(t, c, []string{"web", "auth", "api"})
	})

	t.Run("CorrectTimings", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		// First batch
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

		// Add second batch to empty collector
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

	t.Run("ConcurrentOperations", func(t *testing.T) {
		const (
			writers = 5
			samples = 100
			tags    = 3
			stacks  = 5
		)

		c := newTestCollector()
		baseTime := time.Now()
		expected := make(map[string]expectedResult)
		var mu sync.Mutex
		wg := sync.WaitGroup{}

		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				for j := 0; j < samples; j++ {
					tag := "tag" + strconv.Itoa(id%tags)
					stack := "stack" + strconv.Itoa(j%stacks)
					ts := baseTime.Add(time.Duration(j) * time.Millisecond)

					addSamples(c, []sample{{tag, stack, ts}})

					mu.Lock()
					entry := expected[tag]
					if entry.count == nil {
						entry.count = make(map[string]int)
						entry.startTime = ts
						entry.endTime = ts
					}
					entry.count[stack]++
					if ts.Before(entry.startTime) {
						entry.startTime = ts
					}
					if ts.After(entry.endTime) {
						entry.endTime = ts
					}
					expected[tag] = entry
					mu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		verifyState(t, c, expected)
	})
}
