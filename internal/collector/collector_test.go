package collector_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/collector"
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

// helpers

func addSamples(c *collector.TraceCollector, samples []sample) {
	for _, s := range samples {
		c.AddSample(&collector.Sample{
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

func mergeTagCollections(collected map[string]*collector.TagCollection, tag *collector.TagCollection) {
	if existing, exists := collected[tag.Tags]; exists {
		// Merge counts
		for k, v := range tag.Data {
			existing.Data[k] += v
		}
		// Update time ranges
		if tag.From.Before(existing.From) {
			existing.From = tag.From
		}
		if tag.Until.After(existing.Until) {
			existing.Until = tag.Until
		}
	} else {
		collected[tag.Tags] = tag
	}
}

func generateConcurrentSamples(totalTags, samplesPerTag int) ([]sample, map[string]expectedResult) {
	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := make([]sample, 0, totalTags*samplesPerTag)
	expected := make(map[string]expectedResult)

	for tagID := 0; tagID < totalTags; tagID++ {
		tag := "tag-" + strconv.Itoa(tagID)
		exp := expectedResult{
			count:     make(map[string]int),
			startTime: baseTime.Add(time.Duration(tagID) * time.Millisecond),
			endTime:   baseTime.Add(time.Duration(tagID) * time.Millisecond),
		}

		for sampleID := 0; sampleID < samplesPerTag; sampleID++ {
			stack := "stack-" + strconv.Itoa(sampleID%5)
			sampleTime := exp.startTime.Add(time.Duration(sampleID) * time.Millisecond)

			samples = append(samples, sample{
				tag:   tag,
				stack: stack,
				time:  sampleTime,
			})

			exp.count[stack]++
			if sampleTime.After(exp.endTime) {
				exp.endTime = sampleTime
			}
		}
		expected[tag] = exp
	}

	return samples, expected
}

// tests

func TestTagCollection_DataToBuffer(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	tests := []struct {
		name     string
		tc       collector.TagCollection
		expected []string
	}{
		{
			name:     "empty data",
			tc:       collector.TagCollection{Data: make(map[string]int)},
			expected: []string{},
		},
		{
			name:     "nil data",
			tc:       collector.TagCollection{Data: nil},
			expected: []string{},
		},
		{
			name: "single entry",
			tc: collector.TagCollection{
				Data: map[string]int{"main;login": 5},
			},
			expected: []string{"main;login 5"},
		},
		{
			name: "multiple entries",
			tc: collector.TagCollection{
				Data: map[string]int{
					"http;handler": 3,
					"db;query":     7,
					"cache;get":    2,
				},
			},
			expected: []string{"http;handler 3", "db;query 7", "cache;get 2"},
		},
		{
			name: "with time ranges",
			tc: collector.TagCollection{
				Tags:  "auth",
				From:  now,
				Until: now.Add(time.Hour),
				Data:  map[string]int{"main;logout": 1},
			},
			expected: []string{"main;logout 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := tt.tc.DataToBuffer()
			result := strings.TrimSuffix(buf.String(), "\n")

			var actual []string
			if result != "" {
				actual = strings.Split(result, "\n")
			}

			assert.ElementsMatch(t, tt.expected, actual)
		})
	}
}

func TestTraceCollector(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		c := newTestCollector()
		_, ok := c.ConsumeTag()
		assert.False(t, ok)
	})

	t.Run("Len", func(t *testing.T) {
		c := newTestCollector()
		baseTime := time.Now().Truncate(time.Millisecond)

		addSamples(c, []sample{
			{"auth", "main;login", baseTime},
			{"api", "http;handler", baseTime.Add(20 * time.Millisecond)},
		})

		assert.Equal(t, 2, c.Len())

		addSamples(c, []sample{
			{"auth", "main;login", baseTime},
		})

		assert.Equal(t, 2, c.Len())
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

		// adding existing tag won't increase queue len
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
			{"auth", "main;logout", baseTime.Add(30 * time.Millisecond)},
			{"auth", "main;login", baseTime.Add(10 * time.Millisecond)},
			{"api", "http;handler", baseTime.Add(40 * time.Millisecond)},
		})

		verifyState(t, c, map[string]expectedResult{
			"auth": {
				count:     map[string]int{"main;login": 1, "main;logout": 1},
				startTime: baseTime.Add(10 * time.Millisecond),
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

	t.Run("SubscribeConcurrent", func(t *testing.T) {
		const (
			workers       = 5
			totalTags     = 50
			samplesPerTag = 20
			totalSamples  = totalTags * samplesPerTag
		)

		var (
			wgReaders   sync.WaitGroup
			wgWriters   sync.WaitGroup
			mu          sync.Mutex
			collected   = make(map[string]*collector.TagCollection)
			samplesChan = make(chan *collector.Sample, totalSamples)
			ctx         = context.Background()
		)

		c := newTestCollector()
		c.Subscribe(ctx, samplesChan)

		samples, expected := generateConcurrentSamples(totalTags, samplesPerTag)

		wgWriters.Add(workers)
		chunkSize := len(samples) / workers
		for i := 0; i < workers; i++ {
			start := i * chunkSize
			end := (i + 1) * chunkSize
			go func(start, end int) {
				defer wgWriters.Done()
				for _, s := range samples[start:end] {
					samplesChan <- &collector.Sample{
						Tags:  s.tag,
						Trace: s.stack,
						Time:  s.time,
					}
				}
			}(start, end)
		}

		stopReaders := make(chan struct{})
		for i := 0; i < workers; i++ {
			wgReaders.Add(1)
			go func() {
				defer wgReaders.Done()
				for {
					tag, ok := c.ConsumeTag()
					if ok {
						mu.Lock()
						mergeTagCollections(collected, tag)
						mu.Unlock()
						continue
					}
					select {
					case <-stopReaders:
						return
					default:
						time.Sleep(10 * time.Millisecond)
					}
				}
			}()
		}

		wgWriters.Wait()

		go func() {
			for {
				if c.Len() == 0 {
					close(stopReaders)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()

		wgReaders.Wait()

		verifyCollectedData(t, collected, expected)
	})
}
