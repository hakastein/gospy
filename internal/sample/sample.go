package sample

import (
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
	rateHZ  int
	samples map[string]map[uint64]*Sample
	m       sync.RWMutex
}

func sampleHash(s string, tags string) uint64 {
	return xxhash.Sum64String(s + tags)
}

func newCollection(rateHz int) *Collection {
	return &Collection{
		from:    time.Now(),
		samples: make(map[string]map[uint64]*Sample),
		rateHZ:  rateHz,
	}
}

func (sc *Collection) Finish() {
	sc.m.Lock()
	defer sc.m.Unlock()

	sc.until = time.Now()
}

func (sc *Collection) Props() (int64, int64, int) {
	sc.m.RLock()
	defer sc.m.RUnlock()

	return sc.from.Unix(), sc.until.Unix(), sc.rateHZ
}

func (sc *Collection) Samples() map[string]map[uint64]*Sample {
	sc.m.RLock()
	defer sc.m.RUnlock()

	return sc.samples
}

func (sc *Collection) Len() int {
	return len(sc.samples)
}

func (sc *Collection) AddSample(str, tags string) {
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
	foldedStacksChannel chan [2]string,
	collectionChannel chan<- *Collection,
	accumulationInterval time.Duration,
	rateHz int,
) {
	ticker := time.NewTicker(accumulationInterval)
	defer ticker.Stop()
	collection := newCollection(rateHz)

	for {
		select {
		case <-ticker.C:
			if collection.Len() == 0 {
				continue
			}
			collection.Finish()
			collectionChannel <- collection
			collection = newCollection(rateHz)
		case stack, ok := <-foldedStacksChannel:
			if !ok {
				return
			}
			collection.AddSample(stack[0], stack[1])
		}
	}
}
