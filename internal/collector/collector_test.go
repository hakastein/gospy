package collector_test

import (
	"github.com/hakastein/gospy/internal/collector"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTraceCollector initializes a new TraceCollector instance for testing.
func setupTraceCollector() *collector.TraceCollector {
	return collector.NewTraceCollector()
}

// addSamples adds a slice of samples to the TraceCollector.
func addSamples(tc *collector.TraceCollector, samples []types.Sample) {
	for _, sample := range samples {
		tc.AddSample(&sample)
	}
}

// collectAllConsumedData retrieves all available TagCollection from the TraceCollector.
func collectAllConsumedData(tc *collector.TraceCollector) []*collector.TagCollection {
	var consumedData []*collector.TagCollection
	for {
		data := tc.ConsumeTag()
		if data == nil {
			break
		}
		consumedData = append(consumedData, data)
	}
	return consumedData
}

// mapConsumedDataByTag creates a map from tag to TagCollection for easy lookup.
func mapConsumedDataByTag(consumedData []*collector.TagCollection) map[string]*collector.TagCollection {
	dataMap := make(map[string]*collector.TagCollection)
	for _, data := range consumedData {
		dataMap[data.Tags] = data
	}
	return dataMap
}

// TestTraceCollector contains all unit tests for the TraceCollector.
func TestTraceCollector(t *testing.T) {
	t.Run("ConsumeEmpty", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		data := tc.ConsumeTag()
		assert.Nil(t, data, "ConsumeTag should return nil when TraceCollector is empty")
	})

	t.Run("AddSampleUpdateTraceGroup", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		now := time.Now()

		samples := []types.Sample{
			{Tags: "tagX", Trace: "traceX", Time: now},
			{Tags: "tagX", Trace: "traceY", Time: now.Add(time.Minute)},
		}

		addSamples(tc, samples)

		data := tc.ConsumeTag()
		require.NotNil(t, data, "Expected TagCollection to be non-nil")
		require.Equal(t, "tagX", data.Tags, "Tags should match")

		// Verify time range.
		assert.Equal(t, now, data.From, "From time should be the first sample time")
		assert.Equal(t, now.Add(time.Minute), data.Until, "Until time should be the latest sample time")

		// Verify trace counts.
		expectedStacks := map[string]int{
			"traceX": 1,
			"traceY": 1,
		}
		assert.Equal(t, expectedStacks, data.Data, "Stack counts should match for tagX")
	})

	t.Run("SequentialReadWrite", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		now := time.Now()
		samples := []types.Sample{
			{Tags: "tag1", Trace: "trace1", Time: now},
			{Tags: "tag1", Trace: "trace1", Time: now.Add(time.Second)},
			{Tags: "tag2", Trace: "trace2", Time: now.Add(2 * time.Second)},
		}

		addSamples(tc, samples)

		expectedData := map[string]struct {
			From   time.Time
			Until  time.Time
			Traces map[string]int
		}{
			"tag1": {
				From:  now,
				Until: now.Add(time.Second),
				Traces: map[string]int{
					"trace1": 2,
				},
			},
			"tag2": {
				From:  now.Add(2 * time.Second),
				Until: now.Add(2 * time.Second),
				Traces: map[string]int{
					"trace2": 1,
				},
			},
		}

		consumedData := collectAllConsumedData(tc)
		consumedDataMap := mapConsumedDataByTag(consumedData)

		// Verify the number of tags consumed matches the expected number.
		require.Equal(t, len(expectedData), len(consumedDataMap), "Number of tags should match")

		// Validate each consumed tag's data against the expected data.
		for expectedTag, expected := range expectedData {
			data, exists := consumedDataMap[expectedTag]
			require.True(t, exists, "Tag %s should exist in consumed data", expectedTag)

			assert.Equal(t, expected.From, data.From, "From time should match for tag %s", expectedTag)
			assert.Equal(t, expected.Until, data.Until, "Until time should match for tag %s", expectedTag)

			assert.Equal(t, expected.Traces, data.Data, "Trace counts should match for tag %s", expectedTag)
		}

		// Ensure no additional data is present.
		data := tc.ConsumeTag()
		assert.Nil(t, data, "ConsumeTag should return nil when TraceCollector is empty")
	})

	t.Run("ConcurrentReadWrite", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		startTime := time.Now()

		numWriters := 20
		numConsumers := 10
		samplesPerWriter := 1000
		tags := []string{"tagA", "tagB", "tagC", "tagD", "tagE", "tagF", "tagG", "tagH", "tagI", "tagJ"}

		var writersWG sync.WaitGroup
		writersWG.Add(numWriters)

		doneWriting := make(chan struct{})

		expectedCounts := make(map[string]map[string]int)
		var mutex sync.Mutex

		// Writer function to simulate concurrent sample additions.
		writer := func(writerID int) {
			defer writersWG.Done()
			for i := 0; i < samplesPerWriter; i++ {
				tag := tags[writerID%len(tags)]
				trace := "trace" + strconv.Itoa(i%50)
				sample := types.Sample{
					Tags:  tag,
					Trace: trace,
					Time:  startTime.Add(time.Duration(writerID*samplesPerWriter+i) * time.Millisecond),
				}
				tc.AddSample(&sample)

				mutex.Lock()
				if _, exists := expectedCounts[tag]; !exists {
					expectedCounts[tag] = make(map[string]int)
				}
				expectedCounts[tag][trace]++
				mutex.Unlock()
			}
		}

		// Start writer goroutines.
		for i := 0; i < numWriters; i++ {
			go writer(i)
		}

		actualCounts := make(map[string]map[string]int)
		var consumedDataMutex sync.Mutex

		// Consumer function to simulate concurrent data consumption.
		consumer := func() {
			for {
				data := tc.ConsumeTag()
				if data != nil {
					consumedDataMutex.Lock()
					if _, exists := actualCounts[data.Tags]; !exists {
						actualCounts[data.Tags] = make(map[string]int)
					}
					for trace, count := range data.Data {
						actualCounts[data.Tags][trace] += count
					}
					consumedDataMutex.Unlock()
				} else {
					select {
					case <-doneWriting:
						return
					default:
						time.Sleep(10 * time.Millisecond)
					}
				}
			}
		}

		var consumersWG sync.WaitGroup
		consumersWG.Add(numConsumers)

		// Start consumer goroutines.
		for i := 0; i < numConsumers; i++ {
			go func() {
				defer consumersWG.Done()
				consumer()
			}()
		}

		// Wait for all writers to finish and signal consumers to stop.
		writersWG.Wait()
		close(doneWriting)
		consumersWG.Wait()

		// Verify the number of tags matches the expected count.
		assert.Equal(t, len(expectedCounts), len(actualCounts), "Number of tags should match")

		// Validate each tag's trace counts.
		for tag, expectedStacks := range expectedCounts {
			actualStacks, exists := actualCounts[tag]
			require.True(t, exists, "Tag %s should exist in consumed data", tag)
			assert.Equal(t, expectedStacks, actualStacks, "Stack counts should match for tag %s", tag)
		}
	})

	t.Run("ReadDuringWrites", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		startTime := time.Now()

		numWriters := 10
		samplesPerWriter := 500
		tags := []string{"tag1", "tag2", "tag3", "tag4", "tag5"}

		var writersWG sync.WaitGroup
		writersWG.Add(numWriters)

		expectedCounts := make(map[string]map[string]int)
		var mutex sync.Mutex

		// Writer function for concurrent additions.
		writer := func(writerID int) {
			defer writersWG.Done()
			for i := 0; i < samplesPerWriter; i++ {
				tag := tags[writerID%len(tags)]
				trace := "trace" + strconv.Itoa(i%100)
				sample := types.Sample{
					Tags:  tag,
					Trace: trace,
					Time:  startTime.Add(time.Duration(writerID*samplesPerWriter+i) * time.Millisecond),
				}
				tc.AddSample(&sample)

				mutex.Lock()
				if _, exists := expectedCounts[tag]; !exists {
					expectedCounts[tag] = make(map[string]int)
				}
				expectedCounts[tag][trace]++
				mutex.Unlock()
			}
		}

		// Start writer goroutines.
		for i := 0; i < numWriters; i++ {
			go writer(i)
		}

		actualCounts := make(map[string]map[string]int)
		var consumedDataMutex sync.Mutex

		doneWriting := make(chan struct{})

		// Consumer function to handle data consumption during writes.
		consumer := func() {
			for {
				data := tc.ConsumeTag()
				if data != nil {
					consumedDataMutex.Lock()
					if _, exists := actualCounts[data.Tags]; !exists {
						actualCounts[data.Tags] = make(map[string]int)
					}
					for trace, count := range data.Data {
						actualCounts[data.Tags][trace] += count
					}
					consumedDataMutex.Unlock()
				} else {
					select {
					case <-doneWriting:
						return
					default:
						time.Sleep(5 * time.Millisecond)
					}
				}
			}
		}

		var consumerWG sync.WaitGroup
		consumerWG.Add(5) // Number of concurrent consumers

		// Start consumer goroutines.
		for i := 0; i < 5; i++ {
			go func() {
				defer consumerWG.Done()
				consumer()
			}()
		}

		// Wait for all writers to finish and signal consumers to stop.
		go func() {
			writersWG.Wait()
			close(doneWriting)
		}()

		consumerWG.Wait()

		// Verify the number of tags matches the expected count.
		assert.Equal(t, len(expectedCounts), len(actualCounts), "Number of tags should match")

		// Validate each tag's trace counts.
		for tag, expectedStacks := range expectedCounts {
			actualStacks, exists := actualCounts[tag]
			require.True(t, exists, "Tag %s should exist in consumed data", tag)
			assert.Equal(t, expectedStacks, actualStacks, "Stack counts should match for tag %s", tag)
		}
	})
}

// BenchmarkTraceCollector_AddSample benchmarks the AddSample method of TraceCollector.
func BenchmarkTraceCollector_AddSample(b *testing.B) {
	tc := collector.NewTraceCollector()
	startTime := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sample := types.Sample{
			Tags:  "benchmarkTag",
			Trace: "benchmarkStack",
			Time:  startTime.Add(time.Duration(i) * time.Millisecond),
		}
		tc.AddSample(&sample)
	}
}

// BenchmarkTraceCollector_ConsumeTag benchmarks the ConsumeTag method of TraceCollector.
func BenchmarkTraceCollector_ConsumeTag(b *testing.B) {
	tc := collector.NewTraceCollector()
	startTime := time.Now()

	// Pre-populate the TraceCollector with samples.
	for i := 0; i < b.N; i++ {
		sample := types.Sample{
			Tags:  "benchmarkTag",
			Trace: "benchmarkStack",
			Time:  startTime.Add(time.Duration(i) * time.Millisecond),
		}
		tc.AddSample(&sample)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.ConsumeTag()
	}
}
