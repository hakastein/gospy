package target_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/target"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time {
	return epoch.Add(offset)
}

func process(pid int) target.Process {
	return target.Process{PID: pid, StartTime: 1000}
}

// keepOrder is a tie-break that leaves the candidate order as the scan handed it over.
func keepOrder(n int) int {
	return n - 1
}

func newPlanner(slots int, rotate time.Duration) *target.Planner {
	return target.New(target.Config{Slots: slots, Rotate: rotate, Rand: keepOrder})
}

// traced gives every process a history: one scan attaches them all (the planner must have
// the slots for it) and each attach ends at its own time. A process missing from a later scan
// counts as exited, so every scan of a test has to keep listing the processes it cares about.
func traced(t *testing.T, planner *target.Planner, start time.Time, ends map[target.Process]time.Time) {
	t.Helper()

	processes := make([]target.Process, 0, len(ends))
	for process := range ends {
		processes = append(processes, process)
	}

	plan := planner.Plan(processes, start)
	require.ElementsMatch(t, processes, plan.Attach, "the history scan needs a slot per process")
	for process, end := range ends {
		planner.Ended(process, end, false)
	}
}

func attachedOnly(processes ...target.Process) target.Plan {
	return target.Plan{Attach: processes, Matched: len(processes)}
}

func TestPlanFillsFreeSlots(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		slots   int
		matched []target.Process
		want    target.Plan
	}{
		{
			name:    "no processes",
			slots:   2,
			matched: nil,
			want:    target.Plan{},
		},
		{
			name:    "fewer processes than slots",
			slots:   3,
			matched: []target.Process{process(1), process(2)},
			want:    attachedOnly(process(1), process(2)),
		},
		{
			name:    "more processes than slots",
			slots:   2,
			matched: []target.Process{process(1), process(2), process(3), process(4)},
			want:    target.Plan{Attach: []target.Process{process(1), process(2)}, Matched: 4},
		},
		{
			name:    "no slots",
			slots:   0,
			matched: []target.Process{process(1)},
			want:    target.Plan{Matched: 1},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			planner := newPlanner(tc.slots, time.Minute)

			require.Equal(t, tc.want, planner.Plan(tc.matched, at(0)))
			require.Equal(t, len(tc.want.Attach), planner.Attached())
		})
	}
}

func TestPlanKeepsAnAttachUntilItEnds(t *testing.T) {
	t.Parallel()

	planner := newPlanner(1, 0)
	matched := []target.Process{process(1), process(2)}

	require.Equal(t, []target.Process{process(1)}, planner.Plan(matched, at(0)).Attach)
	require.Empty(t, planner.Plan(matched, at(time.Second)).Attach, "a live attach holds its slot")
	require.Equal(t, 1, planner.Attached())

	planner.Ended(process(1), at(2*time.Second), false)
	require.Equal(t, 0, planner.Attached())
	require.Equal(t, []target.Process{process(2)}, planner.Plan(matched, at(3*time.Second)).Attach, "the freed slot goes to the waiting process")
}

func TestPlanOrdersCandidates(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		history func(t *testing.T, planner *target.Planner)
		matched []target.Process
		want    []target.Process
	}{
		{
			name: "never traced before anything else",
			history: func(t *testing.T, planner *target.Planner) {
				traced(t, planner, at(0), map[target.Process]time.Time{
					process(1): at(time.Second),
					process(2): at(3 * time.Second),
				})
			},
			matched: []target.Process{process(1), process(2), process(3)},
			want:    []target.Process{process(3), process(1), process(2)},
		},
		{
			name: "oldest last-traced time first",
			history: func(t *testing.T, planner *target.Planner) {
				traced(t, planner, at(0), map[target.Process]time.Time{
					process(1): at(30 * time.Second),
					process(2): at(10 * time.Second),
					process(3): at(20 * time.Second),
				})
			},
			matched: []target.Process{process(1), process(2), process(3)},
			want:    []target.Process{process(2), process(3), process(1)},
		},
		{
			name: "a failed attach counts as traced",
			history: func(t *testing.T, planner *target.Planner) {
				plan := planner.Plan([]target.Process{process(1)}, at(0))
				require.Equal(t, []target.Process{process(1)}, plan.Attach)
				planner.Ended(process(1), at(time.Second), true)
			},
			matched: []target.Process{process(1), process(2)},
			want:    []target.Process{process(2), process(1)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			planner := newPlanner(len(tc.matched), 0)
			tc.history(t, planner)

			require.Equal(t, tc.want, planner.Plan(tc.matched, at(time.Hour)).Attach)
		})
	}
}

func TestPlanBreaksTiesAtRandom(t *testing.T) {
	t.Parallel()

	matched := []target.Process{process(1), process(2), process(3), process(4), process(5)}

	order := func(seed uint64) []target.Process {
		source := rand.New(rand.NewPCG(seed, seed))
		planner := target.New(target.Config{Slots: len(matched), Rand: source.IntN})

		return planner.Plan(matched, at(0)).Attach
	}

	require.Equal(t, order(7), order(7), "the same source must give the same order")

	distinct := map[string]struct{}{}
	for seed := uint64(1); seed <= 20; seed++ {
		key := ""
		for _, process := range order(seed) {
			key += string(rune('0' + process.PID))
		}
		distinct[key] = struct{}{}
	}
	require.Greater(t, len(distinct), 1, "processes with equal priority must not always attach in scan order")
}

func TestPlanNeverRotatesWithoutAWaitingProcess(t *testing.T) {
	t.Parallel()

	const rotate = 30 * time.Second

	planner := newPlanner(1, rotate)
	only := []target.Process{process(1)}
	require.Equal(t, only, planner.Plan(only, at(0)).Attach)

	require.Empty(t, planner.Plan(only, at(rotate+time.Minute)).Detach, "without a waiting process there is nothing to rotate for")
	require.Equal(t, 1, planner.Attached())
}

func TestPlanRotatesForAWaitingProcess(t *testing.T) {
	t.Parallel()

	const rotate = 30 * time.Second

	planner := newPlanner(1, rotate)
	traced(t, planner, at(0), map[target.Process]time.Time{process(2): at(0)})

	waiting := []target.Process{process(1), process(2)}
	require.Equal(t, []target.Process{process(1)}, planner.Plan(waiting, at(time.Second)).Attach, "a process never traced goes first")

	require.Empty(t, planner.Plan(waiting, at(time.Second+rotate)).Detach, "an attach exactly as old as the period is not rotated yet")

	plan := planner.Plan(waiting, at(2*time.Second+rotate))
	require.Equal(t, []target.Detach{{Process: process(1), Reason: target.Rotation}}, plan.Detach)
	require.Empty(t, plan.Attach, "the slot is still taken until the detach ends")
	require.Equal(t, 1, planner.Attached())

	planner.Ended(process(1), at(3*time.Second+rotate), false)
	require.Equal(t, []target.Process{process(2)}, planner.Plan(waiting, at(4*time.Second+rotate)).Attach)
}

func TestPlanNeverRotatesWithoutAPeriod(t *testing.T) {
	t.Parallel()

	planner := newPlanner(1, 0)
	traced(t, planner, at(0), map[target.Process]time.Time{process(2): at(0)})

	waiting := []target.Process{process(1), process(2)}
	require.Equal(t, []target.Process{process(1)}, planner.Plan(waiting, at(time.Second)).Attach)

	require.Empty(t, planner.Plan(waiting, at(24*time.Hour)).Detach)
}

func TestPlanDetachesAtMostOnePerScan(t *testing.T) {
	t.Parallel()

	const rotate = 10 * time.Second

	planner := newPlanner(2, rotate)
	traced(t, planner, at(0), map[target.Process]time.Time{process(3): at(0), process(4): at(0)})

	matched := []target.Process{process(1), process(2), process(3), process(4)}
	require.Equal(t, []target.Process{process(1), process(2)}, planner.Plan(matched, at(time.Second)).Attach)

	plan := planner.Plan(matched, at(time.Minute))
	require.Len(t, plan.Detach, 1, "two attaches past the period and two waiting processes still rotate one at a time")
	require.Equal(t, target.Rotation, plan.Detach[0].Reason)

	require.Empty(t, planner.Plan(matched, at(time.Minute+time.Second)).Detach, "a detach in flight blocks the next one")

	planner.Ended(plan.Detach[0].Process, at(time.Minute+2*time.Second), false)
	next := planner.Plan(matched, at(time.Minute+3*time.Second))
	require.Len(t, next.Attach, 1)
	require.Len(t, next.Detach, 1, "once the slot changed hands the next attach past the period rotates")
}

func TestPlanPreemptsForAProcessNeverTraced(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		rotate time.Duration
		// newcomer is whether the waiting process has no history.
		newcomer   bool
		age        time.Duration
		wantDetach []target.Detach
	}{
		{
			name:       "a newcomer waits out the floor",
			newcomer:   true,
			age:        target.PreemptionFloor - time.Millisecond,
			wantDetach: nil,
		},
		{
			name:       "a newcomer pre-empts at the floor",
			newcomer:   true,
			age:        target.PreemptionFloor,
			wantDetach: []target.Detach{{Process: process(1), Reason: target.Preemption}},
		},
		{
			name:       "a newcomer pre-empts before the rotation period",
			rotate:     time.Hour,
			newcomer:   true,
			age:        time.Minute,
			wantDetach: []target.Detach{{Process: process(1), Reason: target.Preemption}},
		},
		{
			name:       "a process traced before waits for a rotation",
			rotate:     time.Hour,
			newcomer:   false,
			age:        time.Minute,
			wantDetach: nil,
		},
		{
			name:       "a process traced before never pre-empts without a period",
			newcomer:   false,
			age:        24 * time.Hour,
			wantDetach: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			planner := newPlanner(1, tc.rotate)
			matched := []target.Process{process(1)}
			if !tc.newcomer {
				traced(t, planner, at(0), map[target.Process]time.Time{process(2): at(0)})
				matched = append(matched, process(2))
			}
			require.Equal(t, []target.Process{process(1)}, planner.Plan(matched, at(time.Second)).Attach)

			plan := planner.Plan([]target.Process{process(1), process(2)}, at(time.Second+tc.age))
			require.Equal(t, tc.wantDetach, plan.Detach)
		})
	}
}

func TestPlanHoldsAFailedProcess(t *testing.T) {
	t.Parallel()

	planner := newPlanner(1, time.Minute)
	only := []target.Process{process(1)}

	failAt := func(start time.Time) {
		t.Helper()
		require.Equal(t, only, planner.Plan(only, start).Attach)
		planner.Ended(process(1), start, true)
	}
	heldAt := func(now time.Time, reason string) {
		t.Helper()
		plan := planner.Plan(only, now)
		require.Empty(t, plan.Attach, reason)
		require.Equal(t, 1, plan.Held, reason)
	}

	failAt(at(0))
	heldAt(at(30*time.Second-time.Millisecond), "the first failure holds the process for 30 seconds")

	failAt(at(30 * time.Second))
	heldAt(at(30*time.Second+59*time.Second), "the second failure doubles the hold")

	failAt(at(90 * time.Second))
	heldAt(at(90*time.Second+5*time.Minute), "the third failure in a row retires the process")
	heldAt(at(24*time.Hour), "a retired process is not retried for its lifetime")
}

func TestPlanResetsTheHoldAfterASuccessfulAttach(t *testing.T) {
	t.Parallel()

	planner := newPlanner(1, time.Minute)
	only := []target.Process{process(1)}

	require.Equal(t, only, planner.Plan(only, at(0)).Attach)
	planner.Ended(process(1), at(0), true)

	require.Equal(t, only, planner.Plan(only, at(30*time.Second)).Attach)
	planner.Ended(process(1), at(40*time.Second), false)

	require.Equal(t, only, planner.Plan(only, at(41*time.Second)).Attach)
	planner.Ended(process(1), at(41*time.Second), true)

	require.Empty(t, planner.Plan(only, at(41*time.Second+29*time.Second)).Attach)
	require.Equal(t, only, planner.Plan(only, at(41*time.Second+30*time.Second)).Attach, "a clean attach in between opens a new streak with the base hold")
}

func TestPlanForgetsAProcessThatExited(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		reappear target.Process
	}{
		{
			name:     "the same identity is treated as new",
			reappear: target.Process{PID: 1, StartTime: 1000},
		},
		{
			name:     "a reused pid is a new process",
			reappear: target.Process{PID: 1, StartTime: 2000},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			planner := newPlanner(1, 0)
			retired := target.Process{PID: 1, StartTime: 1000}
			for i := range 3 {
				start := at(time.Duration(i) * time.Hour)
				require.Equal(t, []target.Process{retired}, planner.Plan([]target.Process{retired}, start).Attach)
				planner.Ended(retired, start, true)
			}
			require.Equal(t, 1, planner.Plan([]target.Process{retired}, at(4*time.Hour)).Held)

			require.Equal(t, target.Plan{}, planner.Plan(nil, at(5*time.Hour)), "a scan without the process is its exit")

			plan := planner.Plan([]target.Process{tc.reappear}, at(6*time.Hour))
			require.Equal(t, []target.Process{tc.reappear}, plan.Attach)
			require.Zero(t, plan.Held)
		})
	}
}

func TestPlanPrefersAReusedPIDAsNeverTraced(t *testing.T) {
	t.Parallel()

	planner := newPlanner(2, 0)
	old := target.Process{PID: 1, StartTime: 1000}
	traced(t, planner, at(0), map[target.Process]time.Time{old: at(time.Second), process(2): at(3 * time.Second)})

	reused := target.Process{PID: 1, StartTime: 5000}
	plan := planner.Plan([]target.Process{process(2), reused}, at(time.Minute))
	require.Equal(t, []target.Process{reused, process(2)}, plan.Attach, "a new start time under an old pid has no history and goes first")
}

func TestEndedIgnoresAProcessThatIsNotAttached(t *testing.T) {
	t.Parallel()

	planner := newPlanner(1, 0)
	planner.Ended(process(1), at(0), true)

	plan := planner.Plan([]target.Process{process(1)}, at(time.Second))
	require.Equal(t, attachedOnly(process(1)), plan, "a stray end report leaves no hold behind")
}
