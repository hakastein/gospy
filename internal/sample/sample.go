package sample

import (
	"context"
	"github.com/cespare/xxhash/v2"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample represents a profiling Sample with its occurrence count
type Sample struct {
	sample string
	count  int
}

func (s *Sample) String() string {
	var sb strings.Builder

	sb.WriteString(s.sample)
	sb.WriteByte(' ')
	sb.WriteString(strconv.Itoa(s.count))

	return sb.String()
}

// Collection groups samples by tags and time intervals
type Collection struct {
	from    time.Time
	until   time.Time
	samples map[string]map[uint64]*Sample
	m       sync.RWMutex
}

func sampleHash(s string, tags string) uint64 {
	return xxhash.Sum64String(s + tags)
}

func newCollection() *Collection {
	return &Collection{
		from:    time.Now(),
		samples: make(map[string]map[uint64]*Sample),
	}
}

func (sc *Collection) Props() (int64, int64) {
	sc.m.RLock()
	defer sc.m.RUnlock()

	return sc.from.Unix(), sc.until.Unix()
}

func (sc *Collection) Samples() map[string]map[uint64]*Sample {
	sc.m.RLock()
	defer sc.m.RUnlock()

	return sc.samples
}

func (sc *Collection) finish() {
	sc.m.Lock()
	defer sc.m.Unlock()

	sc.until = time.Now()
}

func (sc *Collection) len() int {
	return len(sc.samples)
}

func (sc *Collection) addSample(str, tags string) {
	sc.m.Lock()
	defer sc.m.Unlock()

	hash := sampleHash(str, tags)

	tagSamples, tagExists := sc.samples[tags]
	if !tagExists {
		tagSamples = make(map[uint64]*Sample)
		sc.samples[tags] = tagSamples
	}

	if sample, exists := tagSamples[hash]; exists {
		sample.count++
	} else {
		tagSamples[hash] = &Sample{
			sample: str,
			count:  1,
		}
	}
}

func FoldedStacksToCollection(
	ctx context.Context,
	foldedStacksChannel chan [2]string,
	collectionChannel chan<- *Collection,
	accumulationInterval time.Duration,
) {
	ticker := time.NewTicker(accumulationInterval)
	defer ticker.Stop()
	collection := newCollection()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if collection.len() == 0 {
				continue
			}

			collection.finish()
			collectionChannel <- collection
			collection = newCollection()
		case stack, ok := <-foldedStacksChannel:
			if !ok {
				return
			}
			collection.addSample(stack[0], stack[1])
		}
	}
}
