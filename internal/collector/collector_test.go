package collector_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
)

func newTestCollector() *collector.TraceCollector {
	return collector.NewTraceCollector()
}

type TagCollection interface {
	Tags() string
	From() time.Time
	Until() time.Time
	Data() map[string]int
}

type collectorData struct {
	data  map[string]int
	from  time.Time
	until time.Time
}

// helpers

func addSamples(c *collector.TraceCollector, samples []collector.Sample) {
	for _, s := range samples {
		c.AddSample(&s)
	}
}

func collectTags(c *collector.TraceCollector, count int) map[string]TagCollection {
	result := make(map[string]TagCollection)
	for i := 0; i < count; i++ {
		tag, ok := c.ConsumeTag()
		if !ok {
			break
		}
		result[tag.Tags()] = tag
	}
	return result
}

func verifyCollectedData(t *testing.T, collected map[string]TagCollection, expected map[string]collectorData) {
	t.Helper()

	require.Len(t, collected, len(expected), "Number of tags mismatch")

	for tag, exp := range expected {
		actual, exists := collected[tag]
		require.True(t, exists, "missing tag: %s", tag)

		assert.Equal(t, exp.from, actual.From(), "invalid start time for tag: %s", tag)
		assert.Equal(t, exp.until, actual.Until(), "invalid end time for tag: %s", tag)
		assert.Equal(t, exp.data, actual.Data(), "invalid stack counts for tag: %s", tag)
	}
}

func verifyState(t *testing.T, c *collector.TraceCollector, expected map[string]collectorData) {
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
		actualOrder = append(actualOrder, tag.Tags())
	}

	assert.Equal(t, expectedOrder, actualOrder, "Unexpected tag order")
}

// tests

func TestTraceCollector(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		c := newTestCollector()
		_, ok := c.ConsumeTag()
		assert.False(t, ok)
	})

	t.Run("Len", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []collector.Sample{
			{baseTime, "main;login", "auth"},
			{baseTime.Add(20 * time.Millisecond), "http;handler", "api"},
		})

		assert.Equal(t, 2, c.Len())

		addSamples(c, []collector.Sample{
			{baseTime, "main;login", "auth"},
		})

		assert.Equal(t, 2, c.Len())
	})

	t.Run("MaintainsLRUOrderOnAccess", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []collector.Sample{
			{baseTime, "main;login", "auth"},
			{baseTime.Add(20 * time.Millisecond), "http;handler", "api"},
			{baseTime.Add(10 * time.Millisecond), "main;login", "auth"},
			{baseTime.Add(10 * time.Millisecond), "http;handler", "web"},
		})

		verifyOrder(t, c, []string{"auth", "api"})

		addSamples(c, []collector.Sample{
			{baseTime.Add(20 * time.Millisecond), "main;login", "auth"},
			{baseTime.Add(20 * time.Millisecond), "http;handler", "api"},
		})

		verifyOrder(t, c, []string{"web", "auth", "api"})
	})

	t.Run("AccumulatesCorrectTimeRanges", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []collector.Sample{
			{baseTime, "main;login", "auth"},
			{baseTime.Add(10 * time.Millisecond), "main;login", "auth"},
			{baseTime.Add(20 * time.Millisecond), "http;handler", "api"},
		})

		verifyState(t, c, map[string]collectorData{
			"auth": {
				data:  map[string]int{"main;login": 2},
				from:  baseTime,
				until: baseTime.Add(10 * time.Millisecond),
			},
		})

		addSamples(c, []collector.Sample{
			{baseTime.Add(30 * time.Millisecond), "main;logout", "auth"},
			{baseTime.Add(10 * time.Millisecond), "main;login", "auth"},
			{baseTime.Add(40 * time.Millisecond), "http;handler", "api"},
		})

		verifyState(t, c, map[string]collectorData{
			"auth": {
				data:  map[string]int{"main;login": 1, "main;logout": 1},
				from:  baseTime.Add(10 * time.Millisecond),
				until: baseTime.Add(30 * time.Millisecond),
			},
			"api": {
				data:  map[string]int{"http;handler": 2},
				from:  baseTime.Add(20 * time.Millisecond),
				until: baseTime.Add(40 * time.Millisecond),
			},
		})
	})
}

func TestTraceCollector_Subscribe(t *testing.T) {
	t.Run("SimpleWrite", func(t *testing.T) {
		ctx := context.Background()
		samplesChan := make(chan *collector.Sample)
		baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		c := newTestCollector()
		c.Subscribe(ctx, samplesChan)

		samplesChan <- &collector.Sample{
			Tags:  "tag1",
			Trace: "trace1",
			Time:  baseTime,
		}

		assert.Equal(t, 1, c.Len(), "Write into subscribed channel must increase queue len")
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		samplesChan := make(chan *collector.Sample, 1)
		baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		c := newTestCollector()
		c.Subscribe(ctx, samplesChan)

		cancel()
		// wait some to be sure
		<-time.After(100 * time.Millisecond)
		samplesChan <- &collector.Sample{
			Tags:  "tag1",
			Trace: "trace1",
			Time:  baseTime,
		}
		<-time.After(100 * time.Millisecond)

		assert.Equal(t, 0, c.Len(), "Write after cancellation mustn't increase queue len")
	})
}

func TestTagCollection(t *testing.T) {
	t.Run("Getters", func(t *testing.T) {
		now := time.Now()
		data := map[string]int{"trace1": 1}
		expectedData := map[string]int{"trace1": 1}
		tc := collector.NewTagCollection(now, now.Add(time.Second), "tags", data)

		assert.Equal(t, "tags", tc.Tags())
		assert.Equal(t, now, tc.From())
		assert.Equal(t, now.Add(time.Second), tc.Until())
		assert.Equal(t, expectedData, tc.Data())
	})

	t.Run("Len", func(t *testing.T) {
		t.Run("Empty", func(t *testing.T) {
			tc := collector.NewTagCollection(time.Time{}, time.Time{}, "", nil)
			assert.Equal(t, 0, tc.Len())

			tc = collector.NewTagCollection(time.Time{}, time.Time{}, "", make(map[string]int))
			assert.Equal(t, 0, tc.Len())
		})

		t.Run("Single", func(t *testing.T) {
			// "trace1 123" -> len is 10
			tc := collector.NewTagCollection(time.Time{}, time.Time{}, "", map[string]int{"trace1": 123})
			assert.Equal(t, 10, tc.Len())
		})

		t.Run("Zero", func(t *testing.T) {
			// "trace1 0" -> len is 8
			tc := collector.NewTagCollection(time.Time{}, time.Time{}, "", map[string]int{"trace1": 0})
			assert.Equal(t, 8, tc.Len())
		})

		t.Run("Multiple", func(t *testing.T) {
			// "trace1 123\ntrace2 45" -> len is 10 + 1 + 9 = 20
			expectedLen := 20
			data := map[string]int{
				"trace1": 123,
				"trace2": 45,
			}
			tc := collector.NewTagCollection(time.Time{}, time.Time{}, "", data)
			assert.Equal(t, expectedLen, tc.Len())
		})
	})
}

// setupCollectorWithData is a helper function to create and pre-populate a collector.
func setupCollectorWithData(numSamples, numTags int) *collector.TraceCollector {
	tc := collector.NewTraceCollector()
	for i := 0; i < numSamples; i++ {
		sample := &collector.Sample{
			Time:  time.Now(),
			Trace: fmt.Sprintf("main;func;%d", i),
			Tags:  fmt.Sprintf("tag%d", i%numTags),
		}
		tc.AddSample(sample)
	}
	return tc
}

func BenchmarkTagCollection_Len(b *testing.B) {
	data := make(map[string]int)
	for i := 0; i < 100; i++ {
		data[fmt.Sprintf("trace;number;%d", i)] = i * i
	}
	tc := collector.NewTagCollection(time.Now(), time.Now(), "tags", data)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = tc.Len()
	}
}

func BenchmarkTraceCollector_AddSample(b *testing.B) {
	numTags := 10
	tc := setupCollectorWithData(1000, numTags)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tc.AddSample(&collector.Sample{
			Time:  time.Now(),
			Trace: "main;new_func",
			Tags:  fmt.Sprintf("tag%d", i%numTags),
		})
	}
}

func BenchmarkTraceCollector_ConsumeTag(b *testing.B) {
	b.ReportAllocs()
	tc := setupCollectorWithData(b.N, b.N)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, ok := tc.ConsumeTag()
			if !ok {
				b.FailNow()
			}
		}
	})
}
