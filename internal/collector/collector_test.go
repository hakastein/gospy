// collector_test.go
package collector

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gospy/internal/types"
)

func parsePyroscopeData(data string) map[string]int {
	result := make(map[string]int)
	lines := strings.Split(strings.TrimSpace(data), "\n")
	for _, line := range lines {
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			continue
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		result[parts[0]] = count
	}
	return result
}

func setupTraceCollector() *TraceCollector {
	return NewTraceCollector()
}

func TestTraceCollector(t *testing.T) {
	t.Run("SequentialReadWrite", func(t *testing.T) {
		t.Parallel()

		tc := setupTraceCollector()
		now := time.Now()
		samples := []types.Sample{
			{Tags: "tag1", Trace: "stack1", Time: now},
			{Tags: "tag1", Trace: "stack1", Time: now.Add(time.Second)},
			{Tags: "tag2", Trace: "stack2", Time: now.Add(2 * time.Second)},
		}

		for _, sample := range samples {
			tc.AddSample(&sample)
		}

		expectedData := map[string]struct {
			From   time.Time
			Until  time.Time
			Stacks map[string]int
		}{
			"tag1": {
				From:  now,
				Until: now.Add(time.Second),
				Stacks: map[string]int{
					"stack1": 2,
				},
			},
			"tag2": {
				From:  now.Add(2 * time.Second),
				Until: now.Add(2 * time.Second),
				Stacks: map[string]int{
					"stack2": 1,
				},
			},
		}

		// Collect all consumed data
		var consumedData []*PyroscopeData
		for {
			data := tc.ConsumeTag()
			if data == nil {
				break
			}
			consumedData = append(consumedData, data)
		}

		// Map consumed data by tag for comparison
		consumedDataMap := make(map[string]*PyroscopeData)
		for _, data := range consumedData {
			consumedDataMap[data.Tags] = data
		}

		// Compare consumed data with expected data
		require.Equal(t, len(expectedData), len(consumedDataMap), "Number of tags should match")

		for expectedTag, expected := range expectedData {
			data, exists := consumedDataMap[expectedTag]
			require.True(t, exists, "Tag %s should exist in consumed data", expectedTag)

			assert.Equal(t, expected.From, data.From, "From time should match for tag %s", expectedTag)
			assert.Equal(t, expected.Until, data.Until, "Until time should match for tag %s", expectedTag)

			stackCounts := parsePyroscopeData(data.Data)
			assert.Equal(t, expected.Stacks, stackCounts, "Stack counts should match for tag %s", expectedTag)
		}

		// Ensure no extra data is present
		data := tc.ConsumeTag()
		assert.Nil(t, data, "expected nil when consuming from empty TraceCollector")
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

		writer := func(writerID int) {
			defer writersWG.Done()
			for i := 0; i < samplesPerWriter; i++ {
				tag := tags[writerID%len(tags)]
				trace := "stack" + strconv.Itoa(i%50)
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

		for i := 0; i < numWriters; i++ {
			go writer(i)
		}

		actualCounts := make(map[string]map[string]int)
		var consumedDataMutex sync.Mutex

		consumer := func() {
			for {
				data := tc.ConsumeTag()
				if data != nil {
					consumedDataMutex.Lock()
					if _, exists := actualCounts[data.Tags]; !exists {
						actualCounts[data.Tags] = make(map[string]int)
					}
					parsedStacks := parsePyroscopeData(data.Data)
					for stack, count := range parsedStacks {
						actualCounts[data.Tags][stack] += count
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
		for i := 0; i < numConsumers; i++ {
			go func() {
				defer consumersWG.Done()
				consumer()
			}()
		}

		writersWG.Wait()
		close(doneWriting)
		consumersWG.Wait()

		assert.Equal(t, len(expectedCounts), len(actualCounts), "Number of tags should match")

		for tag, expectedStacks := range expectedCounts {
			actualStacks, exists := actualCounts[tag]
			require.True(t, exists, "Tag %s should exist in consumed data", tag)
			assert.Equal(t, expectedStacks, actualStacks, "Stack counts should match for tag %s", tag)
		}
	})

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

		sample1 := types.Sample{
			Tags:  "tagX",
			Trace: "stackX",
			Time:  now,
		}
		tc.AddSample(&sample1)

		sample2 := types.Sample{
			Tags:  "tagX",
			Trace: "stackY",
			Time:  now.Add(time.Minute),
		}
		tc.AddSample(&sample2)

		data := tc.ConsumeTag()
		require.NotNil(t, data, "Expected PyroscopeData to be non-nil")
		require.Equal(t, "tagX", data.Tags, "Tags should match")

		assert.Equal(t, now, data.From, "From time should be the first sample time")
		assert.Equal(t, now.Add(time.Minute), data.Until, "Until time should be the latest sample time")

		stackCounts := parsePyroscopeData(data.Data)
		expectedStacks := map[string]int{
			"stackX": 1,
			"stackY": 1,
		}
		assert.Equal(t, expectedStacks, stackCounts, "Stack counts should match for tagX")
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

		for i := 0; i < numWriters; i++ {
			go writer(i)
		}

		actualCounts := make(map[string]map[string]int)
		var consumedDataMutex sync.Mutex

		doneWriting := make(chan struct{})

		consumer := func() {
			for {
				data := tc.ConsumeTag()
				if data != nil {
					consumedDataMutex.Lock()
					if _, exists := actualCounts[data.Tags]; !exists {
						actualCounts[data.Tags] = make(map[string]int)
					}
					parsedStacks := parsePyroscopeData(data.Data)
					for stack, count := range parsedStacks {
						actualCounts[data.Tags][stack] += count
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

		for i := 0; i < 5; i++ {
			go func() {
				defer consumerWG.Done()
				consumer()
			}()
		}

		go func() {
			writersWG.Wait()
			close(doneWriting)
		}()

		consumerWG.Wait()

		assert.Equal(t, len(expectedCounts), len(actualCounts), "Number of tags should match")

		for tag, expectedStacks := range expectedCounts {
			actualStacks, exists := actualCounts[tag]
			require.True(t, exists, "Tag %s should exist in consumed data", tag)
			assert.Equal(t, expectedStacks, actualStacks, "Stack counts should match for tag %s", tag)
		}
	})
}

func BenchmarkTraceCollector_AddSample(b *testing.B) {
	tc := NewTraceCollector()
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

func BenchmarkTraceCollector_ConsumeTag(b *testing.B) {
	tc := NewTraceCollector()
	startTime := time.Now()

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
