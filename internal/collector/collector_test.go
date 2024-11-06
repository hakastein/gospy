package collector

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"gospy/internal/types"
)

// mockSample creates a Sample instance for testing.
func mockSample(tags, trace string, t time.Time) *types.Sample {
	return &types.Sample{
		Tags:  tags,
		Trace: trace,
		Time:  t,
	}
}

func TestTraceCollector(t *testing.T) {
	t.Parallel()

	// Setup function to initialize TraceCollector before each test.
	setup := func() *TraceCollector {
		return NewTraceCollector()
	}

	t.Run("AddSample", func(t *testing.T) {
		t.Parallel()
		tc := setup()

		t.Run("Add single sample", func(t *testing.T) {
			t.Parallel()
			sample := mockSample("tag1", "trace1", time.Now())
			tc.AddSample(sample)

			tc.mu.RLock()
			defer tc.mu.RUnlock()

			if len(tc.traces) != 1 {
				t.Fatalf("expected 1 traceGroup, got %d", len(tc.traces))
			}

			tg, exists := tc.traces["tag1"]
			if !exists {
				t.Fatal("traceGroup for 'tag1' does not exist")
			}

			if count, ok := tg.stacks["trace1"]; !ok || count != 1 {
				t.Fatalf("expected trace1 count to be 1, got %d", count)
			}

			if tc.queue.Len() != 1 {
				t.Fatalf("expected queue length to be 1, got %d", tc.queue.Len())
			}
		})

		t.Run("Add multiple samples with same tag", func(t *testing.T) {
			t.Parallel()
			tc := setup()
			now := time.Now()

			samples := []*types.Sample{
				mockSample("tag1", "trace1", now),
				mockSample("tag1", "trace2", now.Add(time.Second)),
				mockSample("tag1", "trace1", now.Add(2*time.Second)),
			}

			for _, s := range samples {
				tc.AddSample(s)
			}

			tc.mu.RLock()
			defer tc.mu.RUnlock()

			if len(tc.traces) != 1 {
				t.Fatalf("expected 1 traceGroup, got %d", len(tc.traces))
			}

			tg := tc.traces["tag1"]

			if tg.from != now {
				t.Errorf("expected from to be %v, got %v", now, tg.from)
			}

			if count, ok := tg.stacks["trace1"]; !ok || count != 2 {
				t.Errorf("expected trace1 count to be 2, got %d", count)
			}

			if count, ok := tg.stacks["trace2"]; !ok || count != 1 {
				t.Errorf("expected trace2 count to be 1, got %d", count)
			}

			if tc.queue.Len() != 1 {
				t.Fatalf("expected queue length to be 1, got %d", tc.queue.Len())
			}
		})

		t.Run("Add samples with different tags", func(t *testing.T) {
			t.Parallel()
			tc := setup()
			now := time.Now()

			samples := []*types.Sample{
				mockSample("tag1", "trace1", now),
				mockSample("tag2", "trace2", now.Add(time.Second)),
				mockSample("tag3", "trace3", now.Add(2*time.Second)),
			}

			for _, s := range samples {
				tc.AddSample(s)
			}

			tc.mu.RLock()
			defer tc.mu.RUnlock()

			if len(tc.traces) != 3 {
				t.Fatalf("expected 3 traceGroups, got %d", len(tc.traces))
			}

			for _, tag := range []string{"tag1", "tag2", "tag3"} {
				tg, exists := tc.traces[tag]
				if !exists {
					t.Fatalf("traceGroup for '%s' does not exist", tag)
				}
				if tc.queue.Len() != 3 {
					t.Fatalf("expected queue length to be 3, got %d", tc.queue.Len())
				}
				if tg.from != now && tg.from != now.Add(time.Second) && tg.from != now.Add(2*time.Second) {
					t.Errorf("unexpected 'from' time for tag '%s'", tag)
				}
			}
		})
	})

	t.Run("ConsumeTag", func(t *testing.T) {
		t.Parallel()
		tc := setup()
		now := time.Now()

		samples := []*types.Sample{
			mockSample("tag1", "trace1", now),
			mockSample("tag1", "trace2", now),
			mockSample("tag2", "trace3", now.Add(time.Second)),
		}

		for _, s := range samples {
			tc.AddSample(s)
		}

		t.Run("Consume single tag", func(t *testing.T) {
			t.Parallel()
			data := tc.ConsumeTag()
			if data == nil {
				t.Fatal("expected PyroscopeData, got nil")
			}
			if data.Tags != "tag1" {
				t.Errorf("expected Tags to be 'tag1', got '%s'", data.Tags)
			}
			if data.From != now {
				t.Errorf("expected From to be %v, got %v", now, data.From)
			}
			if data.Until.IsZero() {
				t.Errorf("expected Until to be set, got zero")
			}
			expectedData := "trace1 1\ntrace2 1\n"
			if data.Data != expectedData {
				t.Errorf("expected Data to be '%s', got '%s'", expectedData, data.Data)
			}

			// Verify traceGroup is removed
			tc.mu.RLock()
			defer tc.mu.RUnlock()
			if _, exists := tc.traces["tag1"]; exists {
				t.Error("expected 'tag1' traceGroup to be removed")
			}
			if tc.queue.Len() != 1 {
				t.Errorf("expected queue length to be 1, got %d", tc.queue.Len())
			}
		})

		t.Run("Consume all tags in order", func(t *testing.T) {
			t.Parallel()
			tc := setup()
			now := time.Now()

			samples := []*types.Sample{
				mockSample("tag1", "trace1", now),
				mockSample("tag2", "trace2", now.Add(time.Second)),
				mockSample("tag3", "trace3", now.Add(2*time.Second)),
			}

			for _, s := range samples {
				tc.AddSample(s)
			}

			expectedOrder := []string{"tag1", "tag2", "tag3"}
			for _, tag := range expectedOrder {
				data := tc.ConsumeTag()
				if data == nil {
					t.Fatalf("expected PyroscopeData for tag '%s', got nil", tag)
				}
				if data.Tags != tag {
					t.Errorf("expected Tags to be '%s', got '%s'", tag, data.Tags)
				}
			}

			// Queue should be empty
			if tc.queue.Len() != 0 {
				t.Errorf("expected queue length to be 0, got %d", tc.queue.Len())
			}
		})

		t.Run("Consume when queue is empty", func(t *testing.T) {
			t.Parallel()
			tc := setup()
			data := tc.ConsumeTag()
			if data != nil {
				t.Errorf("expected nil, got %v", data)
			}
		})
	})

	t.Run("Concurrent Access", func(t *testing.T) {
		t.Parallel()
		tc := setup()
		var wg sync.WaitGroup

		addSamples := func(tagPrefix string, count int) {
			defer wg.Done()
			for i := 0; i < count; i++ {
				sample := mockSample(tagPrefix, "trace"+strconv.Itoa(i), time.Now())
				tc.AddSample(sample)
			}
		}

		consumeTags := func(count int) {
			defer wg.Done()
			for i := 0; i < count; i++ {
				tc.ConsumeTag()
			}
		}

		wg.Add(3)
		go addSamples("concurrent1", 100)
		go addSamples("concurrent2", 100)
		go consumeTags(200)
		wg.Wait()

		tc.mu.RLock()
		defer tc.mu.RUnlock()
		if tc.queue.Len() != 0 || len(tc.traces) != 0 {
			t.Errorf("expected all traces to be consumed, got queue length %d and traces length %d",
				tc.queue.Len(), len(tc.traces))
		}
	})
}

func TestTraceGroup_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tg   traceGroup
		want string
	}{
		{
			name: "Empty traceGroup",
			tg: traceGroup{
				stacks: make(map[string]int),
			},
			want: "",
		},
		{
			name: "Single stack",
			tg: traceGroup{
				stacks: map[string]int{
					"trace1": 3,
				},
			},
			want: "trace1 3\n",
		},
		{
			name: "Multiple stacks",
			tg: traceGroup{
				stacks: map[string]int{
					"trace1": 1,
					"trace2": 2,
				},
			},
			want: "trace1 1\ntrace2 2\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.tg.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func BenchmarkTraceCollector_AddSample(b *testing.B) {
	tc := NewTraceCollector()
	tags := "benchmarkTag"
	trace := "benchmarkTrace"
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sample := &types.Sample{
			Tags:  tags,
			Trace: trace,
			Time:  now,
		}
		tc.AddSample(sample)
	}
}

func BenchmarkTraceCollector_ConsumeTag(b *testing.B) {
	tc := NewTraceCollector()
	tags := "benchmarkTag"
	trace := "benchmarkTrace"
	now := time.Now()

	// Pre-populate TraceCollector
	for i := 0; i < b.N; i++ {
		sample := &types.Sample{
			Tags:  tags,
			Trace: trace,
			Time:  now,
		}
		tc.AddSample(sample)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tc.ConsumeTag()
	}
}

func TestNewTraceCollector(t *testing.T) {
	t.Parallel()
	tc := NewTraceCollector()

	tc.mu.RLock()
	defer tc.mu.RUnlock()

	if tc.traces == nil {
		t.Error("expected traces to be initialized, got nil")
	}

	if tc.queue == nil {
		t.Error("expected queue to be initialized, got nil")
	}

	if len(tc.traces) != 0 {
		t.Errorf("expected traces length to be 0, got %d", len(tc.traces))
	}

	if tc.queue.Len() != 0 {
		t.Errorf("expected queue length to be 0, got %d", tc.queue.Len())
	}
}
