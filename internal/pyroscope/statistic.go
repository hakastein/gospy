package pyroscope

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type StatsReport struct {
	TotalRequests   int
	TotalBytes      int
	SuccessRequests int
	FailedRequests  int
	Errors          map[string]int
}

// StatsAggregator manages statistics collection and reporting
type StatsAggregator struct {
	statsChan <-chan *SendResult
	reports   chan StatsReport
	interval  time.Duration
	wg        sync.WaitGroup
}

// NewStatsAggregator creates a new statistics aggregator
func NewStatsAggregator(statsChan <-chan *SendResult, interval time.Duration) *StatsAggregator {
	return &StatsAggregator{
		statsChan: statsChan,
		reports:   make(chan StatsReport),
		interval:  interval,
	}
}

// Start begins the statistics aggregation process
func (sa *StatsAggregator) Start(ctx context.Context) {
	sa.wg.Add(1)
	go func() {
		defer sa.wg.Done()
		sa.run(ctx)
	}()
}

func (sa *StatsAggregator) Wait() {
	sa.wg.Wait()
}

func (sa *StatsAggregator) Reports() <-chan StatsReport {
	return sa.reports
}

// run is the main aggregation loop - extracted for easier testing
func (sa *StatsAggregator) run(ctx context.Context) {
	ticker := time.NewTicker(sa.interval)
	defer ticker.Stop()
	defer close(sa.reports)

	report := StatsReport{
		Errors: make(map[string]int),
	}

	for {
		select {
		case stat, ok := <-sa.statsChan:
			if !ok {
				sa.flush(report)
				return
			}
			report.TotalRequests++
			report.TotalBytes += stat.Bytes
			if stat.Err == nil {
				report.SuccessRequests++
			} else {
				report.FailedRequests++
			}
			if stat.Err != nil {
				report.Errors[stat.Err.Error()]++
			}
		case <-ticker.C:
			sa.flush(report)
			if report.TotalRequests > 0 {
				report = StatsReport{Errors: make(map[string]int)}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (sa *StatsAggregator) flush(report StatsReport) {
	if report.TotalRequests == 0 {
		return
	}

	sa.reports <- cloneStatsReport(report)
}

func cloneStatsReport(report StatsReport) StatsReport {
	cloned := report
	cloned.Errors = make(map[string]int, len(report.Errors))
	for key, value := range report.Errors {
		cloned.Errors[key] = value
	}

	return cloned
}

func LogStatsReports(ctx context.Context, reports <-chan StatsReport) {
	for {
		select {
		case report, ok := <-reports:
			if !ok {
				return
			}
			log.Info().
				Int("total_requests", report.TotalRequests).
				Int("total_bytes", report.TotalBytes).
				Int("success_requests", report.SuccessRequests).
				Int("failed_requests", report.FailedRequests).
				Interface("errors", report.Errors).
				Msg("pyroscope sending statistics")
		case <-ctx.Done():
			return
		}
	}
}
