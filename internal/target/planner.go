// Package target plans which processes of one Target hold its Slots: a pure state machine
// that is told what a Scan found and how every Attach ended, and answers with the attaches to
// start and the ones to detach.
package target

import (
	"math/rand/v2"
	"slices"
	"time"
)

// The pre-emption floor and the one-detach-per-scan limit are constants: a newcomer never
// interrupts an attach that has barely paid for its objdump run, and a php-fpm reload never
// turns into an attach storm.
const (
	PreemptionFloor = 5 * time.Second

	holdBase    = 30 * time.Second
	maxFailures = 3
)

// Process identifies one process for its whole life: a reused PID has a later start time
// and is a new process with no history.
type Process struct {
	PID       int
	StartTime uint64
}

// Config sizes a target's planner. Rand returns a value in [0, n) and only exists so that
// tests can pin the tie-break; nil takes the global source.
type Config struct {
	Slots  int
	Rotate time.Duration
	Rand   func(n int) int
}

// DetachReason says why a Plan ends an attach.
type DetachReason uint8

const (
	// Rotation ends an attach that reached the target's rotation period while a process waited.
	Rotation DetachReason = iota
	// Preemption ends an attach so that a process never traced before gets its first slot.
	Preemption
	// Departure ends an attach whose process the scan no longer lists for this target: it
	// exited, or its command line now belongs to another target.
	Departure
)

func (reason DetachReason) String() string {
	switch reason {
	case Rotation:
		return "rotation"
	case Preemption:
		return "preemption"
	case Departure:
		return "departure"
	default:
		return "unknown"
	}
}

// Detach names an attach to end and why.
type Detach struct {
	Process Process
	Reason  DetachReason
}

// Plan is the answer to one scan. Attach lists processes to start phpspy for, in the order
// the free slots were handed out; Detach holds every departure plus at most one rotation or
// pre-emption per scan.
type Plan struct {
	Attach []Process
	Detach []Detach
	// Failed are the processes phpspy exited on that a scan started later still lists.
	Failed []Process
	// Matched is how many processes the scan brought, Held how many of them sit in a failure hold.
	Matched int
	Held    int
}

type attach struct {
	since     time.Time
	detaching bool
	// exited is when phpspy returned 0; the slot stays taken until a scan settles it.
	exited time.Time
}

type history struct {
	lastTraced time.Time
	failures   int
	holdUntil  time.Time
	retired    bool
}

// Planner is not safe for concurrent use: one goroutine drives a target.
type Planner struct {
	cfg      Config
	attaches map[Process]*attach
	history  map[Process]*history
}

// New returns a planner with no attaches and no history.
func New(cfg Config) *Planner {
	if cfg.Rand == nil {
		cfg.Rand = rand.IntN
	}

	return &Planner{
		cfg:      cfg,
		attaches: make(map[Process]*attach),
		history:  make(map[Process]*history),
	}
}

// Plan counts every process in Attach as attached, and every Detach as in flight, until Ended
// or Exited reports it.
func (planner *Planner) Plan(matched []Process, scanStarted, now time.Time) Plan {
	present := make(map[Process]struct{}, len(matched))
	for _, process := range matched {
		present[process] = struct{}{}
	}

	plan := Plan{Matched: len(matched)}
	plan.Failed = planner.settleExits(present, scanStarted)

	planner.forgetGone(present)

	plan.Detach = planner.departures(present)

	candidates, neverTraced := planner.candidates(matched, now, &plan.Held)

	if free := planner.cfg.Slots - len(planner.attaches); free > 0 && len(candidates) > 0 {
		planner.order(candidates)
		for _, process := range candidates[:min(free, len(candidates))] {
			planner.attaches[process] = &attach{since: now}
			plan.Attach = append(plan.Attach, process)
		}
		candidates = candidates[min(free, len(candidates)):]
	}

	if len(candidates) == 0 || planner.detaching() {
		return plan
	}

	if oldest, ok := planner.oldest(); ok {
		switch {
		case neverTraced && now.Sub(planner.attaches[oldest].since) >= PreemptionFloor:
			planner.attaches[oldest].detaching = true
			plan.Detach = append(plan.Detach, Detach{Process: oldest, Reason: Preemption})
		case planner.cfg.Rotate > 0 && now.Sub(planner.attaches[oldest].since) > planner.cfg.Rotate:
			planner.attaches[oldest].detaching = true
			plan.Detach = append(plan.Detach, Detach{Process: oldest, Reason: Rotation})
		}
	}

	return plan
}

// Ended frees the slot of process. A failed attach opens or extends its hold: 30 seconds,
// doubling per consecutive failure; the third failure in a row retires the process until it
// exits. Any other end resets the count. Either way now becomes the process's last-traced time.
func (planner *Planner) Ended(process Process, now time.Time, failed bool) {
	if _, live := planner.attaches[process]; !live {
		return
	}
	delete(planner.attaches, process)

	record := planner.history[process]
	if record == nil {
		record = &history{}
		planner.history[process] = record
	}
	record.lastTraced = now

	if !failed {
		record.failures = 0
		return
	}

	record.failures++
	if record.failures >= maxFailures {
		record.retired = true
		return
	}

	record.holdUntil = now.Add(holdBase << (record.failures - 1))
}

// Exited keeps the slot taken: phpspy returns 0 once the traced process is gone, but a scan
// started earlier may still list it, so only a scan started after exited settles it.
func (planner *Planner) Exited(process Process, exited time.Time) {
	if attach, live := planner.attaches[process]; live {
		attach.exited = exited
	}
}

func (planner *Planner) settleExits(present map[Process]struct{}, scanStarted time.Time) []Process {
	var failed []Process
	for process, attach := range planner.attaches {
		if attach.exited.IsZero() || !scanStarted.After(attach.exited) {
			continue
		}

		_, alive := present[process]
		planner.Ended(process, attach.exited, alive)
		if alive {
			failed = append(failed, process)
		}
	}

	return failed
}

// Attached is the number of slots in use, detaches in flight included.
func (planner *Planner) Attached() int {
	return len(planner.attaches)
}

// departures are the live attaches whose process the scan no longer lists: it exited, and
// phpspy is ending on its own, or it now belongs to another target and must not be traced twice.
func (planner *Planner) departures(present map[Process]struct{}) []Detach {
	var detaches []Detach
	for process, attach := range planner.attaches {
		if _, listed := present[process]; listed || attach.detaching || !attach.exited.IsZero() {
			continue
		}

		attach.detaching = true
		detaches = append(detaches, Detach{Process: process, Reason: Departure})
	}

	return detaches
}

// candidates are the matched processes that hold no slot and sit in no hold, in scan order,
// and whether any of them was never traced.
func (planner *Planner) candidates(matched []Process, now time.Time, held *int) ([]Process, bool) {
	candidates := make([]Process, 0, len(matched))
	neverTraced := false
	for _, process := range matched {
		if _, live := planner.attaches[process]; live {
			continue
		}

		record := planner.history[process]
		if record != nil && (record.retired || now.Before(record.holdUntil)) {
			*held++
			continue
		}

		neverTraced = neverTraced || record == nil
		candidates = append(candidates, process)
	}

	return candidates, neverTraced
}

// order sorts candidates never traced first, then oldest last-traced first, with ties broken
// at random.
func (planner *Planner) order(candidates []Process) {
	planner.shuffle(candidates)
	slices.SortStableFunc(candidates, func(a, b Process) int {
		return planner.priority(a).Compare(planner.priority(b))
	})
}

// priority is the last-traced time; a process without one sorts first.
func (planner *Planner) priority(process Process) time.Time {
	if record := planner.history[process]; record != nil {
		return record.lastTraced
	}

	return time.Time{}
}

func (planner *Planner) shuffle(processes []Process) {
	for i := len(processes) - 1; i > 0; i-- {
		j := planner.cfg.Rand(i + 1)
		processes[i], processes[j] = processes[j], processes[i]
	}
}

func (planner *Planner) detaching() bool {
	for _, attach := range planner.attaches {
		if attach.detaching {
			return true
		}
	}

	return false
}

// oldest is the longest-running attach; ties resolve to the lowest PID so a plan is repeatable.
func (planner *Planner) oldest() (Process, bool) {
	var (
		oldest Process
		found  bool
	)
	for process, attach := range planner.attaches {
		if !attach.exited.IsZero() {
			continue
		}
		if !found || attach.since.Before(planner.attaches[oldest].since) ||
			(attach.since.Equal(planner.attaches[oldest].since) && process.PID < oldest.PID) {
			oldest, found = process, true
		}
	}

	return oldest, found
}

// forgetGone drops the history of processes that exited: a retired process is retried only
// as a new process, and a reused PID starts with no history.
func (planner *Planner) forgetGone(present map[Process]struct{}) {
	for process := range planner.history {
		_, live := planner.attaches[process]
		if _, seen := present[process]; !seen && !live {
			delete(planner.history, process)
		}
	}
}
