package collector

import (
	"container/list"
	"sync"
	"time"

	"gospy/internal/types"
)

// TagCollection represents the Data of traces categorized by Tags over a period of time.
type TagCollection struct {
	Tags  string
	Data  map[string]int
	From  time.Time
	Until time.Time
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

// ConsumeTag removes the oldest tag from the traces collection and returns its data.
// If there are no tags, it returns nil.
func (tc *TraceCollector) ConsumeTag() *TagCollection {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	elem := tc.queue.Front()
	if elem == nil {
		return nil
	}

	tags := elem.Value.(string)
	tg := tc.traces[tags]

	tc.queue.Remove(elem)
	delete(tc.traces, tags)

	return &TagCollection{
		From:  tg.from,
		Until: tg.until,
		Tags:  tags,
		Data:  tg.stacks,
	}
}

// AddSample increments the sample count in a traceGroup for a given stack and updates access order.
func (tc *TraceCollector) AddSample(stack *types.Sample) {
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
