package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"time"

	"github.com/rs/zerolog"

	"github.com/hakastein/gospy/internal/attach"
	"github.com/hakastein/gospy/internal/config"
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
	// exits are the attaches whose phpspy returned 0 and whose slot waits for the next scan:
	// a process still listed then did not die, so that exit was not the end of its life.
	exits    map[target.Process]*classified
	snapshot *classified
	stats    statisticsReport
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
		exits: make(map[target.Process]*classified),
	}
}

func (runner *targetRunner) run(ctx context.Context) {
	ticker := time.NewTicker(runner.cfg.ScanInterval)
	defer ticker.Stop()

	var statistics <-chan time.Time
	if interval := runner.profiling.cfg.StatsInterval; interval > 0 {
		statsTicker := time.NewTicker(interval)
		defer statsTicker.Stop()
		statistics = statsTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			runner.detachAll()
			return
		case <-ticker.C:
			runner.scan(time.Now())
		case end := <-runner.ended:
			runner.finish(end, time.Now())
		case <-statistics:
			runner.logStatistics()
		}
	}
}

func (runner *targetRunner) scan(now time.Time) {
	latest := runner.profiling.latest.Load()
	if latest == nil {
		return
	}

	matched := latest.byTarget[runner.index]
	if latest != runner.snapshot {
		runner.snapshot = latest
		runner.settleExits(matched, now)
	}

	plan := runner.planner.Plan(matched, now)

	for _, detach := range plan.Detach {
		runner.logger.Debug().Int("pid", detach.Process.PID).Stringer("reason", detach.Reason).Msg("detaching phpspy")
		runner.attaches[detach.Process].Detach()
		runner.stats.detached(detach.Reason)
	}

	for _, process := range plan.Attach {
		runner.start(process, now)
	}

	runner.stats.matched, runner.stats.held = plan.Matched, plan.Held
}

// settleExits reports the attaches that ended with phpspy's exit 0 against a scan taken after
// it. Absent from it, the process died and the slot is simply free; still listed, it lives
// on and the exit counts as a failed attach, so a phpspy that keeps returning 0 on a live
// process is held and retired instead of re-run on every scan.
func (runner *targetRunner) settleExits(matched []target.Process, now time.Time) {
	listed := make(map[target.Process]struct{}, len(matched))
	for _, process := range matched {
		listed[process] = struct{}{}
	}

	for process, seen := range runner.exits {
		if seen == runner.snapshot {
			continue
		}
		delete(runner.exits, process)

		_, alive := listed[process]
		runner.planner.Ended(process, now, alive)
		if alive {
			runner.stats.failed++
			runner.logger.Warn().Int("pid", process.PID).Msg("phpspy exited as if the process had ended, but it is still running")
		}
	}
}

func (runner *targetRunner) start(process target.Process, now time.Time) {
	handle, err := attach.Start(runner.profiling.parseCtx, attach.Config{
		Executable:     runner.profiling.executable,
		PID:            process.PID,
		Rate:           runner.cfg.Rate,
		Args:           runner.cfg.PhpspyArgs,
		Parser:         runner.cfg.Parser,
		Logger:         runner.logger.With().Int("pid", process.PID).Logger(),
		SilenceTimeout: runner.profiling.cfg.SilentAttachTimeout,
	}, runner.profiling.samples)
	if err != nil {
		runner.planner.Ended(process, now, true)
		runner.stats.failed++
		runner.reportStartFailure(process, err)

		return
	}

	runner.logger.Debug().Int("pid", process.PID).Msg("phpspy attached")
	runner.attaches[process] = handle

	go func() {
		runner.ended <- attachEnd{process: process, result: handle.Wait()}
	}()
}

// reportStartFailure tells a broken image apart from a moment without resources. A binary
// that is missing, cannot be executed or has lost its interpreter ends gospy, as the startup
// check would have; a fork or pipe that failed for lack of PIDs, memory or descriptors is one
// failed attach, held and retried like any other.
func (runner *targetRunner) reportStartFailure(process target.Process, err error) {
	if runner.profiling.parseCtx.Err() != nil {
		return
	}

	_, lookupErr := exec.LookPath(runner.profiling.executable)
	if lookupErr != nil || permanentStartFailure(err) {
		runner.profiling.fail(fmt.Errorf("target %s: %w", runner.cfg.Name, err))
		return
	}

	runner.logger.Warn().Int("pid", process.PID).Err(err).Msg("phpspy could not be started, holding the process")
}

func permanentStartFailure(err error) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return true
	}

	for _, code := range permanentStartErrnos {
		if errors.Is(err, code) {
			return true
		}
	}

	return false
}

func (runner *targetRunner) finish(end attachEnd, now time.Time) {
	delete(runner.attaches, end.process)

	event := runner.logger.Debug()
	if end.result.Outcome == attach.Failed {
		event = runner.logger.Warn()
	}
	event.Int("pid", end.process.PID).
		Stringer("outcome", end.result.Outcome).
		AnErr("error", end.result.Err).
		Msg("attach ended")

	if end.result.Outcome == attach.Exited {
		runner.exits[end.process] = runner.snapshot
		return
	}

	failed := end.result.Outcome == attach.Failed
	runner.planner.Ended(end.process, now, failed)
	if failed {
		runner.stats.failed++
	}
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
	runner.logger.Info().
		Int("matched", runner.stats.matched).
		Int("attached", runner.planner.Attached()).
		Int("rotations", runner.stats.rotations).
		Int("preemptions", runner.stats.preemptions).
		Int("failed_attaches", runner.stats.failed).
		Int("held", runner.stats.held).
		Msg("target statistics")

	runner.stats.rotations, runner.stats.preemptions, runner.stats.failed = 0, 0, 0
}

// statisticsReport is what one target reports per interval: the gauges keep their last
// value, the counters cover one report interval.
type statisticsReport struct {
	matched, held                  int
	rotations, preemptions, failed int
}

func (stats *statisticsReport) detached(reason target.DetachReason) {
	switch reason {
	case target.Rotation:
		stats.rotations++
	case target.Preemption:
		stats.preemptions++
	case target.Departure:
	}
}
