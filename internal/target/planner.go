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
	holdCap     = 5 * time.Minute
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

// DetachReason says why a Plan hands a slot to someone else.
type DetachReason uint8

const (
	// Rotation ends an attach that reached the target's rotation period while a process waited.
	Rotation DetachReason = iota
	// Preemption ends an attach so that a process never traced before gets its first slot.
	Preemption
)

// Detach names an attach to end and why.
type Detach struct {
	Process Process
	Reason  DetachReason
}

// Plan is the answer to one scan. Attach lists processes to start phpspy for, in the order
// the free slots were handed out; Detach holds at most one entry per scan.
type Plan struct {
	Attach []Process
	Detach []Detach
	// Matched is how many processes the scan brought, Held how many of them sit in a failure hold.
	Matched int
	Held    int
}

type attach struct {
	since     time.Time
	detaching bool
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

// Plan takes the processes of one scan that belong to this target and decides what to do at
// now. Every process it returns in Attach counts as attached from now on until Ended reports
// it; every Detach counts as in flight until then too.
func (planner *Planner) Plan(matched []Process, now time.Time) Plan {
	planner.forgetGone(matched)

	plan := Plan{Matched: len(matched)}
	candidates := planner.candidates(matched, now, &plan.Held)

	for len(candidates) > 0 && len(planner.attaches) < planner.cfg.Slots {
		process := candidates[0]
		candidates = candidates[1:]

		planner.attaches[process] = &attach{since: now}
		plan.Attach = append(plan.Attach, process)
	}

	if len(candidates) == 0 || planner.detaching() {
		return plan
	}

	if oldest, ok := planner.oldest(); ok {
		switch {
		case planner.neverTraced(candidates) && now.Sub(planner.attaches[oldest].since) >= PreemptionFloor:
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
// doubling per consecutive failure, capped at the rotation period or five minutes, whichever
// is larger; the third failure in a row retires the process until it exits. Any other end
// resets the count. Either way now becomes the process's last-traced time.
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

	hold := holdBase << (record.failures - 1)
	record.holdUntil = now.Add(min(hold, max(planner.cfg.Rotate, holdCap)))
}

// Attached is the number of slots in use, detaches in flight included.
func (planner *Planner) Attached() int {
	return len(planner.attaches)
}

// candidates are the matched processes that hold no slot and sit in no hold, ordered never
// traced first, then oldest last-traced first, with ties broken at random.
func (planner *Planner) candidates(matched []Process, now time.Time, held *int) []Process {
	candidates := make([]Process, 0, len(matched))
	for _, process := range matched {
		if _, live := planner.attaches[process]; live {
			continue
		}

		if record := planner.history[process]; record != nil && (record.retired || now.Before(record.holdUntil)) {
			*held++
			continue
		}

		candidates = append(candidates, process)
	}

	planner.shuffle(candidates)
	slices.SortStableFunc(candidates, func(a, b Process) int {
		return planner.priority(a).Compare(planner.priority(b))
	})

	return candidates
}

// priority is the last-traced time; a process without one sorts first.
func (planner *Planner) priority(process Process) time.Time {
	if record := planner.history[process]; record != nil {
		return record.lastTraced
	}

	return time.Time{}
}

func (planner *Planner) neverTraced(candidates []Process) bool {
	return len(candidates) > 0 && planner.history[candidates[0]] == nil
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
		if !found || attach.since.Before(planner.attaches[oldest].since) ||
			(attach.since.Equal(planner.attaches[oldest].since) && process.PID < oldest.PID) {
			oldest, found = process, true
		}
	}

	return oldest, found
}

// forgetGone drops the history of processes that exited: a retired process is retried only
// as a new process, and a reused PID starts with no history.
func (planner *Planner) forgetGone(matched []Process) {
	present := make(map[Process]struct{}, len(matched))
	for _, process := range matched {
		present[process] = struct{}{}
	}

	for process := range planner.history {
		_, live := planner.attaches[process]
		if _, seen := present[process]; !seen && !live {
			delete(planner.history, process)
		}
	}
}
