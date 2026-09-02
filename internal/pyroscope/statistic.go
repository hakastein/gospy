package pyroscope

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const sendResultBuffer = 1000

type sendResult struct {
	bytes int
	err   error
}

type statsReport struct {
	totalRequests   int
	totalBytes      int
	successRequests int
	failedRequests  int
	errors          map[string]int
}

// A nil *statistics is the disabled mode: record and stop do nothing.
type statistics struct {
	results  chan *sendResult
	done     chan struct{}
	interval time.Duration
	logger   zerolog.Logger
	stopOnce sync.Once
}

func startStatistics(ctx context.Context, interval time.Duration, logger zerolog.Logger) *statistics {
	stats := &statistics{
		results:  make(chan *sendResult, sendResultBuffer),
		done:     make(chan struct{}),
		interval: interval,
		logger:   logger,
	}

	go stats.run(ctx)

	return stats
}

func (stats *statistics) record(ctx context.Context, bytes int, err error) {
	if stats == nil {
		return
	}

	select {
	case stats.results <- &sendResult{bytes: bytes, err: err}:
	case <-ctx.Done():
	}
}

// stop flushes the last report; every producer must have finished recording by then.
func (stats *statistics) stop() {
	if stats == nil {
		return
	}

	stats.stopOnce.Do(func() { close(stats.results) })
	<-stats.done
}

func (stats *statistics) run(ctx context.Context) {
	defer close(stats.done)

	ticker := time.NewTicker(stats.interval)
	defer ticker.Stop()

	report := newStatsReport()

	for {
		select {
		case result, ok := <-stats.results:
			if !ok {
				stats.flush(report)
				return
			}
			report.add(result)
		case <-ticker.C:
			stats.flush(report)
			report = newStatsReport()
		case <-ctx.Done():
			return
		}
	}
}

func (stats *statistics) flush(report statsReport) {
	if report.totalRequests == 0 {
		return
	}

	stats.logger.Info().
		Int("total_requests", report.totalRequests).
		Int("total_bytes", report.totalBytes).
		Int("success_requests", report.successRequests).
		Int("failed_requests", report.failedRequests).
		Interface("errors", report.errors).
		Msg("pyroscope sending statistics")
}

func newStatsReport() statsReport {
	return statsReport{errors: make(map[string]int)}
}

func (report *statsReport) add(result *sendResult) {
	report.totalRequests++
	report.totalBytes += result.bytes

	if result.err == nil {
		report.successRequests++
		return
	}

	report.failedRequests++
	report.errors[result.err.Error()]++
}
