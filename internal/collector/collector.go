package collector

import (
	"container/list"
	"context"
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

type traceGroup struct {
	stacks map[string]int
	from   time.Time
	until  time.Time
}

// traceCollector cuts the oldest accumulated batch first.
type traceCollector struct {
	traces map[string]*traceGroup
	queue  *list.List
}

// Collect closes batches once samples is closed and drained.
func Collect(ctx context.Context, samples <-chan *Sample, batches chan<- *TagCollection) {
	defer close(batches)

	tc := &traceCollector{
		traces: make(map[string]*traceGroup),
		queue:  list.New(),
	}

	var pending *TagCollection
	for {
		// A batch is cut once the input looks momentarily empty - best-effort, a sample can
		// arrive right after the check - so a burst ships as one request, not one per sample.
		if pending == nil && len(samples) == 0 {
			pending = tc.consume()
		}

		if pending == nil && samples == nil {
			return
		}

		var output chan<- *TagCollection
		if pending != nil {
			output = batches
		}

		select {
		case <-ctx.Done():
			log.Info().Msg("collector shutting down")
			return
		case output <- pending:
			pending = nil
		case sample, ok := <-samples:
			if !ok {
				samples = nil
				continue
			}
			tc.add(sample)
		}
	}
}

func (tc *traceCollector) consume() *TagCollection {
	elem := tc.queue.Front()
	if elem == nil {
		return nil
	}

	tags := elem.Value.(string)
	tg := tc.traces[tags]

	tc.queue.Remove(elem)
	delete(tc.traces, tags)

	return NewTagCollection(tg.from, tg.until, tags, tg.stacks)
}

func (tc *traceCollector) add(sample *Sample) {
	tg, exists := tc.traces[sample.Tags]
	if !exists {
		tg = &traceGroup{
			stacks: make(map[string]int),
			from:   sample.Time,
			until:  sample.Time,
		}
		tc.traces[sample.Tags] = tg
		tc.queue.PushBack(sample.Tags)
	}

	if sample.Time.After(tg.until) {
		tg.until = sample.Time
	}
	if sample.Time.Before(tg.from) {
		tg.from = sample.Time
	}
	tg.stacks[sample.Trace]++

	log.Trace().
		Str("tags", sample.Tags).
		Str("trace", sample.Trace).
		Int("trace_count", tg.stacks[sample.Trace]).
		Int("queued_tag_groups", tc.queue.Len()).
		Msg("sample added to collector")
}
