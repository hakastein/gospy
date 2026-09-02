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

type pipe struct {
	samples chan *collector.Sample
	batches chan *collector.TagCollection
	done    chan struct{}
}

func startPipe(t *testing.T, ctx context.Context, buffer int) *pipe {
	t.Helper()

	p := &pipe{
		samples: make(chan *collector.Sample),
		batches: make(chan *collector.TagCollection, buffer),
		done:    make(chan struct{}),
	}

	go func() {
		defer close(p.done)
		collector.Collect(ctx, p.samples, p.batches)
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

func TestCollectEmitsBatchPerTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 0)

	p.samples <- sample(baseTime, "main;login", "auth")
	batch := p.next(t)

	assert.Equal(t, "auth", batch.Tags())
	assert.Equal(t, map[string]int{"main;login": 1}, batch.Data())
	assert.Equal(t, baseTime, batch.From())
	assert.Equal(t, baseTime, batch.Until())

	p.samples <- sample(baseTime.Add(time.Second), "http;handler", "api")
	batch = p.next(t)

	assert.Equal(t, "api", batch.Tags())
	assert.Equal(t, map[string]int{"http;handler": 1}, batch.Data())

	close(p.samples)
	require.Empty(t, p.drain(t))
}

func TestCollectAggregatesEverySample(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	p := startPipe(t, ctx, 16)

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

	traces := make(map[string]map[string]int)
	from := make(map[string]time.Time)
	until := make(map[string]time.Time)
	for _, batch := range p.drain(t) {
		tags := batch.Tags()
		if _, seen := traces[tags]; !seen {
			traces[tags] = make(map[string]int)
			from[tags] = batch.From()
			until[tags] = batch.Until()
		}
		for trace, count := range batch.Data() {
			traces[tags][trace] += count
		}
		require.False(t, batch.From().After(batch.Until()), "batch time range must not be inverted")
		if batch.From().Before(from[tags]) {
			from[tags] = batch.From()
		}
		if batch.Until().After(until[tags]) {
			until[tags] = batch.Until()
		}
	}

	assert.Equal(t, map[string]map[string]int{
		"auth": {"main;login": 2, "main;logout": 1},
		"api":  {"http;handler": 2},
	}, traces)
	assert.Equal(t, baseTime, from["auth"])
	assert.Equal(t, baseTime.Add(30*time.Millisecond), until["auth"])
	assert.Equal(t, baseTime.Add(20*time.Millisecond), from["api"])
	assert.Equal(t, baseTime.Add(40*time.Millisecond), until["api"])
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
	collector.Collect(ctx, samples, batches)

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
	p := startPipe(t, ctx, 0)

	for i, tags := range []string{"auth", "api", "web"} {
		p.samples <- sample(baseTime.Add(time.Duration(i)*time.Second), "main;handler", tags)
		assert.Equal(t, tags, p.next(t).Tags())
	}

	close(p.samples)
	require.Empty(t, p.drain(t))
}

func TestCollectClosesOutputWhenInputIsClosed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := startPipe(t, ctx, 1)
	close(p.samples)

	require.Empty(t, p.drain(t))
}

func TestCollectStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	p := startPipe(t, ctx, 0)

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
	done := make(chan struct{})

	go func() {
		defer close(done)
		collector.Collect(context.Background(), samples, batches)
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
