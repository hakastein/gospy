package pyroscope

import (
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const statsEventBuffer = 1000

type statsEvent struct {
	bytes   int
	dropped int
	retries int
	err     error
}

type statsReport struct {
	totalRequests   int
	totalBytes      int
	successRequests int
	retriedAttempts int
	failedRequests  int
	droppedSamples  int
	errors          map[string]int
}

// A nil *statistics is the disabled mode: record, recordDropped and stop do nothing. The loop
// outlives a cancelled context so that the events the cancellation itself causes reach the last report.
type statistics struct {
	events   chan *statsEvent
	done     chan struct{}
	interval time.Duration
	logger   zerolog.Logger
	stopOnce sync.Once
}

func startStatistics(interval time.Duration, logger zerolog.Logger) *statistics {
	stats := &statistics{
		events:   make(chan *statsEvent, statsEventBuffer),
		done:     make(chan struct{}),
		interval: interval,
		logger:   logger,
	}

	go stats.run()

	return stats
}

func (stats *statistics) record(event statsEvent) {
	if stats == nil {
		return
	}

	stats.events <- &event
}

// recordDropped never blocks its caller: counting is worth less than the pipeline it observes.
func (stats *statistics) recordDropped(count int) {
	if stats == nil || count <= 0 {
		return
	}

	select {
	case stats.events <- &statsEvent{dropped: count}:
	default:
	}
}

// stop flushes the last report; every producer must have finished recording by then.
func (stats *statistics) stop() {
	if stats == nil {
		return
	}

	stats.stopOnce.Do(func() { close(stats.events) })
	<-stats.done
}

func (stats *statistics) run() {
	defer close(stats.done)

	ticker := time.NewTicker(stats.interval)
	defer ticker.Stop()

	report := newStatsReport()

	for {
		select {
		case event, ok := <-stats.events:
			if !ok {
				stats.flush(report)
				return
			}
			report.add(event)
		case <-ticker.C:
			stats.flush(report)
			report = newStatsReport()
		}
	}
}

func (stats *statistics) flush(report statsReport) {
	if report.totalRequests == 0 && report.droppedSamples == 0 {
		return
	}

	stats.logger.Info().
		Int("total_requests", report.totalRequests).
		Int("total_bytes", report.totalBytes).
		Int("success_requests", report.successRequests).
		Int("retried_attempts", report.retriedAttempts).
		Int("failed_requests", report.failedRequests).
		Int("dropped_samples", report.droppedSamples).
		Interface("errors", report.errors).
		Msg("pyroscope sending statistics")
}

func newStatsReport() statsReport {
	return statsReport{errors: make(map[string]int)}
}

func (report *statsReport) add(event *statsEvent) {
	if event.dropped > 0 {
		report.droppedSamples += event.dropped
		return
	}

	report.totalRequests++
	report.totalBytes += event.bytes
	report.retriedAttempts += event.retries

	if event.err == nil {
		report.successRequests++
		return
	}

	report.failedRequests++
	report.errors[event.err.Error()]++
}
