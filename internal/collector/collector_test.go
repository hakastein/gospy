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

const dropReportBuffer = 8

type pipe struct {
	samples chan *collector.Sample
	ticks   chan time.Time
	batches chan *collector.TagCollection
	drops   chan int
	done    chan struct{}
}

func startPipe(t *testing.T, ctx context.Context, buffer int, config collector.Config) *pipe {
	t.Helper()

	p := &pipe{
		samples: make(chan *collector.Sample),
		ticks:   make(chan time.Time),
		batches: make(chan *collector.TagCollection, buffer),
		drops:   make(chan int, dropReportBuffer),
		done:    make(chan struct{}),
	}

	config.Ticks = p.ticks
	config.OnDrop = func(count int) { p.drops <- count }

	go func() {
		defer close(p.done)
		collector.Collect(ctx, p.samples, p.batches, config)
	}()

	t.Cleanup(func() {
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("Collect did not return")
		}
	})

	return p
}

// tick cuts every open batch; the send returns once the collector has taken it.
func (p *pipe) tick(t *testing.T) {
	t.Helper()

	select {
	case p.ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("collector did not take the tick")
	}
}

func (p *pipe) next(t *testing.T) *collector.TagCollection {
	t.Helper()

	select {
	case batch, ok := <-p.batches:
		require.True(t, ok, "batches closed before the expected batch")
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("collector emitted no batch")
		return nil
	}
}

func (p *pipe) drain(t *testing.T) []*collector.TagCollection {
	t.Helper()

	var batches []*collector.TagCollection
	for {
		select {
		case batch, ok := <-p.batches:
			if !ok {
				return batches
			}
			batches = append(batches, batch)
		case <-time.After(5 * time.Second):
			t.Fatal("collector did not close its output")
			return nil
		}
	}
}

func sample(at time.Time, trace, tags string) *collector.Sample {
	return &collector.Sample{Time: at, Trace: trace, Tags: tags}
}

func TestCollectCutsABatchPerTagSetOnATick(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 0, collector.Config{})

	p.samples <- sample(baseTime, "main;login", "auth")
	p.samples <- sample(baseTime.Add(time.Second), "main;login", "auth")
	p.samples <- sample(baseTime.Add(2*time.Second), "http;handler", "api")
	p.tick(t)

	batch := p.next(t)
	assert.Equal(t, "auth", batch.Tags())
	assert.Equal(t, map[string]int{"main;login": 2}, batch.Data())
	assert.Equal(t, baseTime, batch.From())
	assert.Equal(t, baseTime.Add(time.Second), batch.Until())

	batch = p.next(t)
	assert.Equal(t, "api", batch.Tags())
	assert.Equal(t, map[string]int{"http;handler": 1}, batch.Data())
	assert.Equal(t, baseTime.Add(2*time.Second), batch.From())
	assert.Equal(t, baseTime.Add(2*time.Second), batch.Until())

	close(p.samples)
	require.Empty(t, p.drain(t))
}

func TestCollectHoldsSamplesUntilTheWindowEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 1, collector.Config{})

	for i := 0; i < 4; i++ {
		p.samples <- sample(baseTime.Add(time.Duration(i)*time.Millisecond), "main;login", "auth")
	}

	select {
	case batch := <-p.batches:
		t.Fatalf("collector cut a batch with no tick: %v", batch.Data())
	default:
	}

	close(p.samples)
	batches := p.drain(t)
	require.Len(t, batches, 1)
	assert.Equal(t, map[string]int{"main;login": 4}, batches[0].Data())
}

func TestCollectAggregatesEverySampleIntoOneBatchPerTagSet(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 16, collector.Config{})

	samples := []*collector.Sample{
		sample(baseTime.Add(10*time.Millisecond), "main;login", "auth"),
		sample(baseTime, "main;login", "auth"),
		sample(baseTime.Add(20*time.Millisecond), "http;handler", "api"),
		sample(baseTime.Add(30*time.Millisecond), "main;logout", "auth"),
		sample(baseTime.Add(40*time.Millisecond), "http;handler", "api"),
	}
	for _, s := range samples {
		p.samples <- s
	}
	close(p.samples)

	batches := p.drain(t)
	require.Len(t, batches, 2)

	assert.Equal(t, "auth", batches[0].Tags())
	assert.Equal(t, map[string]int{"main;login": 2, "main;logout": 1}, batches[0].Data())
	assert.Equal(t, baseTime, batches[0].From())
	assert.Equal(t, baseTime.Add(30*time.Millisecond), batches[0].Until())

	assert.Equal(t, "api", batches[1].Tags())
	assert.Equal(t, map[string]int{"http;handler": 2}, batches[1].Data())
	assert.Equal(t, baseTime.Add(20*time.Millisecond), batches[1].From())
	assert.Equal(t, baseTime.Add(40*time.Millisecond), batches[1].Until())
}

func TestCollectShipsBufferedBurstAsOneBatch(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	samples := make(chan *collector.Sample, 4)
	for i := 0; i < cap(samples); i++ {
		samples <- sample(baseTime.Add(time.Duration(i)*time.Second), "main;login", "auth")
	}
	close(samples)

	batches := make(chan *collector.TagCollection, cap(samples))
	collector.Collect(ctx, samples, batches, collector.Config{})

	batch, ok := <-batches
	require.True(t, ok, "collector emitted no batch")
	assert.Equal(t, map[string]int{"main;login": 4}, batch.Data())
	assert.Equal(t, baseTime, batch.From())
	assert.Equal(t, baseTime.Add(3*time.Second), batch.Until())

	_, ok = <-batches
	assert.False(t, ok, "collector must close its output once drained")
}

func TestCollectEmitsBatchesInTagArrivalOrder(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	tagSets := []string{"auth", "api", "web"}
	p := startPipe(t, ctx, 0, collector.Config{})

	for i, tags := range tagSets {
		p.samples <- sample(baseTime.Add(time.Duration(i)*time.Second), "main;handler", tags)
	}
	p.tick(t)

	for _, tags := range tagSets {
		assert.Equal(t, tags, p.next(t).Tags())
	}

	close(p.samples)
	require.Empty(t, p.drain(t))
}

func TestCollectDropsSamplesBeyondTheTagGroupCap(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 0, collector.Config{MaxTagGroups: 1})

	p.samples <- sample(baseTime, "main;login", "auth")
	p.samples <- sample(baseTime.Add(time.Second), "http;handler", "api")
	p.samples <- sample(baseTime.Add(2*time.Second), "http;handler", "web")
	p.tick(t)

	select {
	case dropped := <-p.drops:
		assert.Equal(t, 2, dropped, "the window's drops must be reported as one aggregated count")
	case <-time.After(5 * time.Second):
		t.Fatal("collector reported no dropped samples")
	}

	batch := p.next(t)
	assert.Equal(t, "auth", batch.Tags())
	assert.Equal(t, map[string]int{"main;login": 1}, batch.Data())

	p.tick(t)
	select {
	case dropped := <-p.drops:
		t.Fatalf("collector reported %d drops again with nothing dropped since", dropped)
	default:
	}

	close(p.samples)
	require.Empty(t, p.drain(t))
}

func TestCollectCutsABatchAtTheStackCap(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 1, collector.Config{MaxStacksPerGroup: 2})

	p.samples <- sample(baseTime, "main;login", "auth")
	p.samples <- sample(baseTime.Add(time.Second), "main;logout", "auth")

	batch := p.next(t)
	assert.Equal(t, "auth", batch.Tags())
	assert.Equal(t, map[string]int{"main;login": 1, "main;logout": 1}, batch.Data())
	assert.Equal(t, baseTime, batch.From())
	assert.Equal(t, baseTime.Add(time.Second), batch.Until())

	p.samples <- sample(baseTime.Add(2*time.Second), "main;login", "auth")
	close(p.samples)

	batches := p.drain(t)
	require.Len(t, batches, 1)
	assert.Equal(t, map[string]int{"main;login": 1}, batches[0].Data())
}

func TestCollectClosesOutputWhenInputIsClosed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := startPipe(t, ctx, 1, collector.Config{})
	close(p.samples)

	require.Empty(t, p.drain(t))
}

func TestCollectStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	p := startPipe(t, ctx, 0, collector.Config{})

	cancel()

	require.Empty(t, p.drain(t), "cancelled collect must not emit batches")
}

func TestTagCollectionGetters(t *testing.T) {
	t.Parallel()

	now := time.Now()
	batch := collector.NewTagCollection(now, now.Add(time.Second), "tags", map[string]int{"trace1": 1})

	assert.Equal(t, "tags", batch.Tags())
	assert.Equal(t, now, batch.From())
	assert.Equal(t, now.Add(time.Second), batch.Until())
	assert.Equal(t, map[string]int{"trace1": 1}, batch.Data())
}

func BenchmarkCollect(b *testing.B) {
	const numTags = 10

	samples := make(chan *collector.Sample)
	batches := make(chan *collector.TagCollection, numTags)
	ticks := make(chan time.Time)
	done := make(chan struct{})

	go func() {
		defer close(done)
		collector.Collect(context.Background(), samples, batches, collector.Config{Ticks: ticks})
	}()
	go func() {
		for range batches {
		}
	}()

	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		samples <- &collector.Sample{
			Time:  now,
			Trace: "main;func",
			Tags:  fmt.Sprintf("tag%d", i%numTags),
		}
	}

	b.StopTimer()
	close(samples)
	<-done
}
