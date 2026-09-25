package collector

import (
	"container/list"
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// The caps bound the memory a stalled consumer can cost: while a batch waits, a
// high-cardinality dynamic tag would otherwise grow the open batches without limit.
const (
	DefaultMaxTagGroups      = 1000
	DefaultMaxStacksPerGroup = 10000
	DefaultMaxPendingBatches = 64
)

// Sample is one observed stack with its tags and the rate, in Hz, it was sampled at.
type Sample struct {
	Time       time.Time
	Trace      string
	Tags       string
	SampleRate int
}

// TagCollection is a Batch: the stacks collected for one tag set at one sample rate over a
// period of time. Tag set and rate together form the key, as Pyroscope converts a batch's
// counts into CPU time with the rate the request declares.
type TagCollection struct {
	tags       string
	sampleRate int
	data       map[string]int
	from       time.Time
	until      time.Time
}

// NewTagCollection builds a batch of the stacks in data, counted per folded stack, that were
// sampled at sampleRate between from and until under one tag set.
func NewTagCollection(from time.Time, until time.Time, tags string, sampleRate int, data map[string]int) *TagCollection {
	return &TagCollection{
		from:       from,
		until:      until,
		tags:       tags,
		sampleRate: sampleRate,
		data:       data,
	}
}

func (tc *TagCollection) Data() map[string]int {
	return tc.data
}

// SampleRate is the rate, in Hz, every stack in the batch was sampled at.
func (tc *TagCollection) SampleRate() int {
	return tc.sampleRate
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

func (tc *TagCollection) SampleCount() int {
	return countSamples(tc.data)
}

func countSamples(stacks map[string]int) int {
	total := 0
	for _, count := range stacks {
		total += count
	}

	return total
}

// Config tunes when batches are cut and how much the collector may hold while a batch waits
// for its consumer. Every tick on Ticks cuts all open batches; a nil Ticks leaves the closing
// of the input as the only time-based cut. A cap at or below zero falls back to its default;
// samples that would exceed one are dropped and counted through OnDrop, once per flush.
type Config struct {
	Ticks             <-chan time.Time
	MaxTagGroups      int
	MaxStacksPerGroup int
	MaxPendingBatches int
	OnDrop            func(count int)
}

type groupKey struct {
	tags       string
	sampleRate int
}

type traceGroup struct {
	stacks map[string]int
	from   time.Time
	until  time.Time
	queued *list.Element
}

// traceCollector cuts the oldest accumulated batch first.
type traceCollector struct {
	config           Config
	groups           map[groupKey]*traceGroup
	queue            *list.List
	ready            *list.List
	droppedTagGroups int
	droppedStacks    int
}

// Collect ships a batch per tag set on every tick, cuts a batch early once its tag set reaches
// the stack cap, and closes batches once samples is closed and drained.
func Collect(ctx context.Context, samples <-chan *Sample, batches chan<- *TagCollection, config Config) {
	defer close(batches)

	tc := newTraceCollector(config)

	for {
		var (
			output chan<- *TagCollection
			batch  *TagCollection
		)
		if front := tc.ready.Front(); front != nil {
			batch = front.Value.(*TagCollection)
			output = batches
		}

		if batch == nil && samples == nil {
			return
		}

		select {
		case <-ctx.Done():
			tc.abandon()
			log.Info().Msg("collector shutting down")
			return
		case output <- batch:
			tc.ready.Remove(tc.ready.Front())
		case <-tc.config.Ticks:
			tc.flush()
		case sample, ok := <-samples:
			if !ok {
				samples = nil
				tc.drain()
				continue
			}
			tc.add(sample)
		}
	}
}

func newTraceCollector(config Config) *traceCollector {
	if config.MaxTagGroups <= 0 {
		config.MaxTagGroups = DefaultMaxTagGroups
	}
	if config.MaxStacksPerGroup <= 0 {
		config.MaxStacksPerGroup = DefaultMaxStacksPerGroup
	}
	if config.MaxPendingBatches <= 0 {
		config.MaxPendingBatches = DefaultMaxPendingBatches
	}

	return &traceCollector{
		config: config,
		groups: make(map[groupKey]*traceGroup),
		queue:  list.New(),
		ready:  list.New(),
	}
}

// flush cuts every open batch, oldest first, unless the consumer is already holding the most
// batches the collector may keep: cutting more of them would only move the memory one list over.
func (tc *traceCollector) flush() {
	if tc.canCut() {
		tc.cutAll()
	}

	tc.reportDropped()
}

// drain cuts every open batch whatever the consumer is doing: the input has nothing left to add.
func (tc *traceCollector) drain() {
	tc.cutAll()
	tc.reportDropped()
}

func (tc *traceCollector) cutAll() {
	for front := tc.queue.Front(); front != nil; front = tc.queue.Front() {
		tc.cut(front.Value.(groupKey))
	}
}

func (tc *traceCollector) canCut() bool {
	return tc.ready.Len() < tc.config.MaxPendingBatches
}

func (tc *traceCollector) cut(key groupKey) {
	group := tc.groups[key]

	tc.queue.Remove(group.queued)
	delete(tc.groups, key)

	tc.ready.PushBack(NewTagCollection(group.from, group.until, key.tags, key.sampleRate, group.stacks))
}

func (tc *traceCollector) abandon() {
	tc.reportDropped()

	held := 0
	for element := tc.ready.Front(); element != nil; element = element.Next() {
		held += element.Value.(*TagCollection).SampleCount()
	}
	for _, group := range tc.groups {
		held += countSamples(group.stacks)
	}

	if held > 0 && tc.config.OnDrop != nil {
		tc.config.OnDrop(held)
	}
}

func (tc *traceCollector) reportDropped() {
	dropped := tc.droppedTagGroups + tc.droppedStacks
	if dropped == 0 {
		return
	}

	log.Warn().
		Int("dropped_samples", dropped).
		Int("over_tag_group_cap", tc.droppedTagGroups).
		Int("over_stack_cap", tc.droppedStacks).
		Msg("collector dropped samples over its caps")

	if tc.config.OnDrop != nil {
		tc.config.OnDrop(dropped)
	}

	tc.droppedTagGroups = 0
	tc.droppedStacks = 0
}

func (tc *traceCollector) add(sample *Sample) {
	key := groupKey{tags: sample.Tags, sampleRate: sample.SampleRate}

	group, exists := tc.groups[key]
	if !exists {
		if len(tc.groups) >= tc.config.MaxTagGroups {
			tc.droppedTagGroups++
			return
		}

		group = &traceGroup{
			stacks: make(map[string]int),
			from:   sample.Time,
			until:  sample.Time,
		}
		tc.groups[key] = group
		group.queued = tc.queue.PushBack(key)
	}

	if _, known := group.stacks[sample.Trace]; !known && len(group.stacks) >= tc.config.MaxStacksPerGroup {
		tc.droppedStacks++
		return
	}

	if sample.Time.After(group.until) {
		group.until = sample.Time
	}
	if sample.Time.Before(group.from) {
		group.from = sample.Time
	}
	group.stacks[sample.Trace]++

	log.Trace().
		Str("tags", sample.Tags).
		Int("sample_rate", sample.SampleRate).
		Str("trace", sample.Trace).
		Int("trace_count", group.stacks[sample.Trace]).
		Int("queued_tag_groups", tc.queue.Len()).
		Msg("sample added to collector")

	if len(group.stacks) >= tc.config.MaxStacksPerGroup && tc.canCut() {
		tc.cut(key)
	}
}
