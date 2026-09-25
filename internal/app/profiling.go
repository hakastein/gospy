package app

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/config"
	"github.com/hakastein/gospy/internal/procscan"
	"github.com/hakastein/gospy/internal/target"
)

// A process reparented this deep under gospy is not one of its helpers any more.
const ancestryDepth = 64

// A scan that fails keeps failing; one line a minute says so without flooding the log.
const scanErrorPeriod = time.Minute

// classified is one scan sorted into targets: byTarget[i] holds the processes that belong to
// the i-th configured target, gospy and its descendants already left out.
type classified struct {
	byTarget [][]target.Process
}

// profiling runs the targets: one scan loop feeding the latest classified snapshot to one
// runner per enabled target, all of them writing into the shared samples channel.
type profiling struct {
	cfg        Config
	executable string
	samples    chan<- *collector.Sample
	parseCtx   context.Context
	stop       context.CancelFunc
	ownPID     int
	latest     atomic.Pointer[classified]
	runners    []*targetRunner

	mu       sync.Mutex
	fatalErr error
}

func newProfiling(cfg Config, executable string, samples chan<- *collector.Sample, parseCtx context.Context, stop context.CancelFunc) *profiling {
	p := &profiling{
		cfg:        cfg,
		executable: executable,
		samples:    samples,
		parseCtx:   parseCtx,
		stop:       stop,
		ownPID:     os.Getpid(),
	}

	for index, cfgTarget := range cfg.Targets {
		logger := log.With().Str("target", cfgTarget.Name).Logger()
		if !cfgTarget.Enabled() {
			logger.Warn().Msg("target disabled: max-processes is 0")
			continue
		}

		logger.Info().
			Int("max_processes", cfgTarget.MaxProcesses).
			Int("rate", cfgTarget.Rate).
			Dur("rotate", cfgTarget.Rotate).
			Dur("scan_interval", cfgTarget.ScanInterval).
			Strs("tags", cfgTarget.Tags).
			Strs("phpspy_args", cfgTarget.PhpspyArgs).
			Msg("target enabled")

		p.runners = append(p.runners, newTargetRunner(p, index, cfgTarget, logger))
	}

	return p
}

// run returns once ctx has ended and every attach with it. A phpspy that cannot be started
// ends ctx itself through stop and is reported by fatal.
func (p *profiling) run(ctx context.Context) {
	if len(p.runners) == 0 {
		log.Warn().Msg("no target is enabled, nothing will be profiled")
		<-ctx.Done()
		return
	}

	var wg sync.WaitGroup
	wg.Go(func() { p.scanLoop(ctx) })
	wg.Go(func() { p.reportStatistics(ctx) })
	for _, runner := range p.runners {
		wg.Go(func() { runner.run(ctx) })
	}
	wg.Wait()
}

// fail records the first fatal error and ends the run.
func (p *profiling) fail(err error) {
	p.mu.Lock()
	if p.fatalErr == nil {
		p.fatalErr = err
	}
	p.mu.Unlock()

	p.stop()
}

func (p *profiling) fatal() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.fatalErr
}

// scanLoop reads the process table once per the shortest enabled scan interval and publishes
// it classified; each runner examines the latest snapshot on its own interval.
func (p *profiling) scanLoop(ctx context.Context) {
	source := p.cfg.processes()
	scanFailures := log.Sample(&zerolog.BurstSampler{Burst: 1, Period: scanErrorPeriod})

	scan := func() {
		processes, err := source.Scan()
		if err != nil {
			scanFailures.Warn().Err(err).Msg("cannot read the process table")
			return
		}

		p.latest.Store(classify(processes, p.cfg.Targets, p.ownPID))
	}

	scan()

	ticker := time.NewTicker(p.scanInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		}
	}
}

func (p *profiling) scanInterval() time.Duration {
	interval := time.Duration(0)
	for _, runner := range p.runners {
		if interval == 0 || runner.cfg.ScanInterval < interval {
			interval = runner.cfg.ScanInterval
		}
	}

	return interval
}

// classify hands every process to the first target in file order whose matchers all hold.
// gospy's own process and everything under it (phpspy, sh, objdump) are never candidates.
func classify(processes []procscan.Process, targets []config.Target, ownPID int) *classified {
	parents := make(map[int]int, len(processes))
	for _, process := range processes {
		parents[process.PID] = process.ParentPID
	}

	result := &classified{byTarget: make([][]target.Process, len(targets))}
	for _, process := range processes {
		if descendsFrom(parents, process.PID, ownPID) {
			continue
		}

		for index, cfgTarget := range targets {
			if !cfgTarget.Match.Matches(process) {
				continue
			}

			result.byTarget[index] = append(result.byTarget[index], target.Process{PID: process.PID, StartTime: process.StartTime})
			break
		}
	}

	return result
}

func descendsFrom(parents map[int]int, pid, ancestor int) bool {
	for depth := 0; depth < ancestryDepth && pid > 0; depth++ {
		if pid == ancestor {
			return true
		}

		parent, known := parents[pid]
		if !known {
			return false
		}
		pid = parent
	}

	return false
}

func (p *profiling) reportStatistics(ctx context.Context) {
	if p.cfg.StatsInterval <= 0 {
		return
	}

	ticker := time.NewTicker(p.cfg.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, runner := range p.runners {
				runner.logStatistics()
			}
		}
	}
}
