package collector

import (
	"container/list"
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type Sample struct {
	Time  time.Time
	Trace string
	Tags  string
}

// TagCollection represents the Data of traces categorized by Tags over a period of time.
type TagCollection struct {
	tags  string
	data  map[string]int
	from  time.Time
	until time.Time
}

func NewTagCollection(from time.Time, until time.Time, tags string, data map[string]int) *TagCollection {
	return &TagCollection{
		from:  from,
		until: until,
		tags:  tags,
		data:  data,
	}
}

func (tc *TagCollection) Len() int {
	if len(tc.data) == 0 {
		return 0
	}
	size := len(tc.data) - 1
	for sample, count := range tc.data {
		size += len(sample)
		size += 1
		if count == 0 {
			size += 1
		}
		c := count
		for c > 0 {
			size++
			c /= 10
		}
	}
	return size
}

func (tc *TagCollection) Data() map[string]int {
	return tc.data
}

func (tc *TagCollection) From() time.Time {
	return tc.from
}

func (tc *TagCollection) Until() time.Time {
	return tc.until
}

func (tc *TagCollection) Tags() string {
	return tc.tags
}

// traceGroup represents a collection of stacks with counts and a time range.
type traceGroup struct {
	stacks        map[string]int
	from          time.Time
	until         time.Time
	queuePosition *list.Element
}

// TraceCollector manages trace groups organized by tags and tracks access order.
type TraceCollector struct {
	mu     sync.RWMutex
	traces map[string]*traceGroup
	queue  *list.List
}

// NewTraceCollector initializes and returns a new TraceCollector.
func NewTraceCollector() *TraceCollector {
	return &TraceCollector{
		traces: make(map[string]*traceGroup),
		queue:  list.New(),
	}
}

func (tc *TraceCollector) Len() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.queue.Len()
}

// ConsumeTag removes the oldest tag from the traces collection and returns its data.
// If there are no tags, it returns nil.
func (tc *TraceCollector) ConsumeTag() (*TagCollection, bool) {
	tc.mu.Lock()

	elem := tc.queue.Front()
	if elem == nil {
		return nil, false
	}

	tags := elem.Value.(string)
	tg := tc.traces[tags]

	tc.queue.Remove(elem)
	delete(tc.traces, tags)

	tc.mu.Unlock()

	return NewTagCollection(
		tg.from,
		tg.until,
		tags,
		tg.stacks,
	), true
}

// AddSample increments the sample count in a traceGroup for a given stack and updates access order.
func (tc *TraceCollector) AddSample(stack *Sample) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tg, exists := tc.traces[stack.Tags]
	if !exists {
		tg = &traceGroup{
			stacks: make(map[string]int),
			from:   stack.Time,
			until:  stack.Time,
		}
		tc.traces[stack.Tags] = tg
		// Push tag into end of the queue
		tg.queuePosition = tc.queue.PushBack(stack.Tags)
	}

	if stack.Time.After(tg.until) {
		tg.until = stack.Time
	}
	if stack.Time.Before(tg.from) {
		tg.from = stack.Time
	}
	tg.stacks[stack.Trace]++
}

// Subscribe starts a goroutine that listens to stacksChannel and adds samples to the TraceCollector.
func (tc *TraceCollector) Subscribe(ctx context.Context, stacksChannel <-chan *Sample) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("shutdown subscriber")
				return
			case sample, ok := <-stacksChannel:
				if !ok {
					return
				}
				tc.AddSample(sample)
			}
		}
	}()
}
