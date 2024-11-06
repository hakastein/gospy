package collector

import (
	"container/list"
	"strconv"
	"strings"
	"sync"
	"time"

	"gospy/internal/types"
)

// PyroscopeData represents the data structure returned by ConsumeTag.
type PyroscopeData struct {
	Data  string
	Tags  string
	From  time.Time
	Until time.Time
}

// traceGroup represents a collection of stacks with counts and a time range.
type traceGroup struct {
	stacks   map[string]int
	from     time.Time
	until    time.Time
	listElem *list.Element // Reference to its position in the access log
}

// String returns a string representation of the traceGroup.
// The order of stacks is not guaranteed.
func (tg traceGroup) String() string {
	var builder strings.Builder
	for stack, count := range tg.stacks {
		builder.WriteString(stack)
		builder.WriteRune(' ')
		builder.WriteString(strconv.Itoa(count))
		builder.WriteRune('\n')
	}
	return builder.String()
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
func (tc *TraceCollector) ConsumeTag() *PyroscopeData {
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

	return &PyroscopeData{
		From:  tg.from,
		Until: tg.until,
		Tags:  tags,
		Data:  tg.String(),
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
		tg.listElem = tc.queue.PushBack(stack.Tags)
	}

	if stack.Time.After(tg.until) {
		tg.until = stack.Time
	}
	if stack.Time.Before(tg.from) {
		tg.from = stack.Time
	}
	tg.stacks[stack.Trace]++
}
