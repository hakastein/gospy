package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/hakastein/gospy/internal/attach"
	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/target"
)

type attachEnd struct {
	process target.Process
	result  attach.Result
}

// targetRunner drives one target: it plans on every scan interval, starts and detaches
// phpspy, and tells the planner how every attach ended. One goroutine owns the planner.
type targetRunner struct {
	profiling *profiling
	index     int
	cfg       config.Target
	logger    zerolog.Logger
	planner   *target.Planner
	attaches  map[target.Process]*attach.Attach
	ended     chan attachEnd
	stats     targetStatistics
}

func newTargetRunner(p *profiling, index int, cfg config.Target, logger zerolog.Logger) *targetRunner {
	return &targetRunner{
		profiling: p,
		index:     index,
		cfg:       cfg,
		logger:    logger,
		planner:   target.New(target.Config{Slots: cfg.MaxProcesses, Rotate: cfg.Rotate}),
		attaches:  make(map[target.Process]*attach.Attach, cfg.MaxProcesses),
		// Every attach reports its end once and the planner never holds more than the slots.
		ended: make(chan attachEnd, cfg.MaxProcesses),
	}
}

func (runner *targetRunner) run(ctx context.Context) {
	ticker := time.NewTicker(runner.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			runner.detachAll()
			return
		case <-ticker.C:
			runner.scan(time.Now())
		case end := <-runner.ended:
			runner.finish(end, time.Now())
		}
	}
}

func (runner *targetRunner) scan(now time.Time) {
	latest := runner.profiling.latest.Load()
	if latest == nil {
		return
	}

	plan := runner.planner.Plan(latest.byTarget[runner.index], now)

	for _, detach := range plan.Detach {
		runner.logger.Debug().Int("pid", detach.Process.PID).Str("reason", reasonName(detach.Reason)).Msg("detaching phpspy")
		runner.attaches[detach.Process].Detach()
		runner.stats.detached(detach.Reason)
	}

	for _, process := range plan.Attach {
		runner.start(process, now)
	}

	runner.stats.scanned(plan.Matched, plan.Held, runner.planner.Attached())
}

func (runner *targetRunner) start(process target.Process, now time.Time) {
	handle, err := attach.Start(runner.profiling.parseCtx, attach.Config{
		Executable: runner.profiling.executable,
		PID:        process.PID,
		Rate:       runner.cfg.Rate,
		Args:       runner.cfg.PhpspyArgs,
		Parser: phpspy.ParserConfig{
			Entrypoints:        runner.cfg.Entrypoints,
			StaticTags:         runner.cfg.StaticTags,
			DynamicTags:        runner.cfg.DynamicTags,
			TagEntrypoint:      runner.cfg.TagEntrypoint,
			KeepEntrypointName: runner.cfg.KeepEntrypointName,
		},
		Logger:         runner.logger.With().Int("pid", process.PID).Logger(),
		SilenceTimeout: runner.profiling.cfg.SilentAttachTimeout,
	}, runner.profiling.samples)
	if err != nil {
		runner.planner.Ended(process, now, true)
		if runner.profiling.parseCtx.Err() == nil {
			runner.profiling.fail(fmt.Errorf("target %s: %w", runner.cfg.Name, err))
		}

		return
	}

	runner.logger.Debug().Int("pid", process.PID).Msg("phpspy attached")
	runner.attaches[process] = handle

	go func() {
		runner.ended <- attachEnd{process: process, result: handle.Wait()}
	}()
}

func (runner *targetRunner) finish(end attachEnd, now time.Time) {
	delete(runner.attaches, end.process)

	failed := end.result.Outcome == attach.Failed
	runner.planner.Ended(end.process, now, failed)
	runner.stats.finished(failed, runner.planner.Attached())

	event := runner.logger.Debug()
	if failed {
		event = runner.logger.Warn()
	}
	event.Int("pid", end.process.PID).
		Str("outcome", end.result.Outcome.String()).
		AnErr("error", end.result.Err).
		Msg("attach ended")
}

// detachAll ends every attach at once and waits for each to finish parsing what it holds.
func (runner *targetRunner) detachAll() {
	for _, handle := range runner.attaches {
		handle.Detach()
	}
	for _, handle := range runner.attaches {
		handle.Wait()
	}
}

func (runner *targetRunner) logStatistics() {
	report := runner.stats.take()

	runner.logger.Info().
		Int("matched", report.matched).
		Int("attached", report.attached).
		Int("rotations", report.rotations).
		Int("preemptions", report.preemptions).
		Int("failed_attaches", report.failed).
		Int("held", report.held).
		Msg("target statistics")
}

func reasonName(reason target.DetachReason) string {
	if reason == target.Preemption {
		return "preemption"
	}

	return "rotation"
}

type statisticsReport struct {
	matched, attached, held        int
	rotations, preemptions, failed int
}

// targetStatistics is written by the runner and read by the statistics reporter: the gauges
// keep their last value, the counters cover one report interval.
type targetStatistics struct {
	mu     sync.Mutex
	report statisticsReport
}

func (stats *targetStatistics) scanned(matched, held, attached int) {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.report.matched = matched
	stats.report.held = held
	stats.report.attached = attached
}

func (stats *targetStatistics) detached(reason target.DetachReason) {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	if reason == target.Preemption {
		stats.report.preemptions++
	} else {
		stats.report.rotations++
	}
}

func (stats *targetStatistics) finished(failed bool, attached int) {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.report.attached = attached
	if failed {
		stats.report.failed++
	}
}

func (stats *targetStatistics) take() statisticsReport {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	report := stats.report
	stats.report.rotations, stats.report.preemptions, stats.report.failed = 0, 0, 0

	return report
}
