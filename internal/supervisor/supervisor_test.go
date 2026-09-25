package supervisor_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/supervisor"
)

// The fake caps stdout lines the way the real profiler does, at a size that keeps tests cheap.
const fakeStdoutLineLimit = 4096

// testTimeout is a safety net: every case here is expected to finish without waiting on a clock.
const testTimeout = 30 * time.Second

// fakeClock replaces the supervisor's clock: it records every delay the supervisor asks for and
// never makes a test wait. A blocked clock hands back a channel that stays silent, which pins
// down the branch a test wants to observe.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	delays  []time.Duration
	blocked bool
	onAfter func(time.Duration)
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0).UTC()}
}

func newBlockedClock() *fakeClock {
	clock := newFakeClock()
	clock.blocked = true
	return clock
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(step time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(step)
}

func (c *fakeClock) After(delay time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.delays = append(c.delays, delay)
	blocked, now, onAfter := c.blocked, c.now, c.onAfter
	c.mu.Unlock()

	if onAfter != nil {
		onAfter(delay)
	}

	if blocked {
		return make(chan time.Time)
	}

	fired := make(chan time.Time, 1)
	fired <- now
	return fired
}

func (c *fakeClock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

// gatedReader serves before, blocks until gate is closed, serves after, then reports EOF once.
// It stands in for a profiler whose stderr stays open past the last line it wrote.
type gatedReader struct {
	before   io.Reader
	gate     <-chan struct{}
	after    io.Reader
	onEOF    func()
	passed   bool
	reported bool
}

func (r *gatedReader) Read(buffer []byte) (int, error) {
	if !r.passed {
		read, err := r.before.Read(buffer)
		if read > 0 {
			return read, nil
		}
		if !errors.Is(err, io.EOF) {
			return 0, err
		}
		<-r.gate
		r.passed = true
	}

	read, err := r.after.Read(buffer)
	if read > 0 {
		return read, nil
	}
	if errors.Is(err, io.EOF) && !r.reported {
		r.reported = true
		if r.onEOF != nil {
			r.onEOF()
		}
	}

	return 0, err
}

// profilerSession scripts one run of the fake profiler. keepsRunning holds Wait until the
// session context is cancelled, the way a profiler blocked writing into an unread pipe does;
// uptime is how far the fake clock moves while the session runs.
type profilerSession struct {
	stdout       string
	stderr       io.Reader
	waitErr      error
	keepsRunning bool
	uptime       time.Duration
}

type fakeProfiler struct {
	t             *testing.T
	clock         *fakeClock
	mu            sync.Mutex
	sessions      []profilerSession
	startCount    int
	waitCount     int
	sessionCtx    context.Context
	terminated    bool
	stderrAtEOF   bool
	waitedAtEOF   []bool
	onStart       func(int)
	stderrEOFSeen bool
}

func (p *fakeProfiler) Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error) {
	p.mu.Lock()
	require.Less(p.t, p.startCount, len(p.sessions), "unexpected Start call")
	session := p.sessions[p.startCount]
	p.startCount++
	startCount := p.startCount
	p.sessionCtx = ctx
	p.stderrAtEOF = false
	onStart := p.onStart
	p.mu.Unlock()

	if onStart != nil {
		onStart(startCount)
	}

	stdout := bufio.NewScanner(strings.NewReader(session.stdout))
	stdout.Buffer(make([]byte, 0, fakeStdoutLineLimit), fakeStdoutLineLimit)

	if session.stderr == nil {
		return stdout, nil, nil
	}

	return stdout, bufio.NewScanner(session.stderr), nil
}

func (p *fakeProfiler) Wait() error {
	p.mu.Lock()
	require.Less(p.t, p.waitCount, len(p.sessions), "unexpected Wait call")
	session := p.sessions[p.waitCount]
	p.waitCount++
	sessionCtx := p.sessionCtx
	p.waitedAtEOF = append(p.waitedAtEOF, p.stderrAtEOF)
	p.mu.Unlock()

	if session.uptime > 0 {
		p.clock.Advance(session.uptime)
	}

	if !session.keepsRunning {
		return session.waitErr
	}

	<-sessionCtx.Done()

	p.mu.Lock()
	p.terminated = true
	p.mu.Unlock()

	return session.waitErr
}

// markStderrEOF is what a scripted stderr stream calls when the supervisor has drained it.
func (p *fakeProfiler) markStderrEOF() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stderrAtEOF = true
	p.stderrEOFSeen = true
}

func (p *fakeProfiler) Starts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCount
}

func (p *fakeProfiler) Terminated() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminated
}

// WaitedAfterStderrEOF reports, per session, whether the supervisor had drained stderr by the
// time it called Wait.
func (p *fakeProfiler) WaitedAfterStderrEOF() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.waitedAtEOF...)
}

func (p *fakeProfiler) StderrDrained() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderrEOFSeen
}

type fakeParser struct{}

func (fakeParser) Parse(context.Context, *bufio.Scanner, chan<- *collector.Sample) error {
	return nil
}

// gateOpeningParser releases a scripted stderr stream when the parser is done with stdout, which
// is the moment the supervisor starts reaping the session.
type gateOpeningParser struct {
	gate chan struct{}
}

func (p gateOpeningParser) Parse(context.Context, *bufio.Scanner, chan<- *collector.Sample) error {
	close(p.gate)
	return nil
}

func failingSessions(count int, err error) []profilerSession {
	sessions := make([]profilerSession, count)
	for index := range sessions {
		sessions[index] = profilerSession{waitErr: err}
	}

	return sessions
}

func TestValidateRestart(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		restart string
		wantErr bool
	}{
		{
			name:    "no",
			restart: supervisor.RestartNo,
		},
		{
			name:    "always",
			restart: supervisor.RestartAlways,
		},
		{
			name:    "onerror",
			restart: supervisor.RestartOnError,
		},
		{
			name:    "onsuccess",
			restart: supervisor.RestartOnSuccess,
		},
		{
			name:    "unknown policy",
			restart: "sometimes",
			wantErr: true,
		},
		{
			name:    "empty text",
			restart: "",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := supervisor.ValidateRestart(tc.restart)
			if tc.wantErr {
				require.ErrorContains(t, err, "invalid restart option")
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestManageProfilerLifecycle(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		restart       string
		sessions      []profilerSession
		cancelOnStart int
		wantErr       error
		expectedRuns  int
	}{
		{
			name:         "graceful exit without restart",
			restart:      supervisor.RestartNo,
			sessions:     []profilerSession{{}},
			expectedRuns: 1,
		},
		{
			name:         "wait error without restart",
			restart:      supervisor.RestartNo,
			sessions:     []profilerSession{{waitErr: errors.New("profiler exited with error")}},
			wantErr:      errors.New("profiler exited with error"),
			expectedRuns: 1,
		},
		{
			name:          "restart on success",
			restart:       supervisor.RestartOnSuccess,
			sessions:      []profilerSession{{}, {}},
			cancelOnStart: 2,
			expectedRuns:  2,
		},
		{
			name:          "restart on error",
			restart:       supervisor.RestartOnError,
			sessions:      []profilerSession{{waitErr: errors.New("first failure")}, {}},
			cancelOnStart: 2,
			expectedRuns:  2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			clock := newFakeClock()
			profiler := &fakeProfiler{
				t:        t,
				clock:    clock,
				sessions: tc.sessions,
				onStart: func(startCount int) {
					if tc.cancelOnStart != 0 && startCount == tc.cancelOnStart {
						cancel()
					}
				},
			}

			policy := supervisor.RestartPolicy{Mode: tc.restart, Now: clock.Now, After: clock.After}
			err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), policy)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr.Error())
			}
			require.Equal(t, tc.expectedRuns, profiler.Starts())
		})
	}
}

func TestManageProfilerBacksOffUntilTheBudgetIsSpent(t *testing.T) {
	t.Parallel()

	startFailure := errors.New("cannot attach to pid 1234")

	testCases := []struct {
		name       string
		policy     supervisor.RestartPolicy
		wantStarts int
		wantDelays []time.Duration
	}{
		{
			name:       "always with the default budget and cap",
			policy:     supervisor.RestartPolicy{Mode: supervisor.RestartAlways},
			wantStarts: 10,
			wantDelays: []time.Duration{
				time.Second,
				2 * time.Second,
				4 * time.Second,
				8 * time.Second,
				16 * time.Second,
				32 * time.Second,
				time.Minute,
				time.Minute,
				time.Minute,
			},
		},
		{
			name: "onerror with a tight budget and cap",
			policy: supervisor.RestartPolicy{
				Mode:                   supervisor.RestartOnError,
				BaseDelay:              10 * time.Millisecond,
				MaxDelay:               40 * time.Millisecond,
				MaxConsecutiveFailures: 5,
			},
			wantStarts: 5,
			wantDelays: []time.Duration{
				10 * time.Millisecond,
				20 * time.Millisecond,
				40 * time.Millisecond,
				40 * time.Millisecond,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			clock := newFakeClock()
			profiler := &fakeProfiler{t: t, clock: clock, sessions: failingSessions(tc.wantStarts, startFailure)}

			policy := tc.policy
			policy.Now, policy.After = clock.Now, clock.After

			err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), policy)

			require.NoError(t, ctx.Err(), "ManageProfiler did not return within the test context")
			require.ErrorIs(t, err, startFailure)
			require.ErrorContains(t, err, "giving up")
			require.Equal(t, tc.wantStarts, profiler.Starts())
			require.Equal(t, tc.wantDelays, clock.Delays())
		})
	}
}

func TestManageProfilerResetsTheBackoff(t *testing.T) {
	t.Parallel()

	failure := errors.New("cannot attach to pid 1234")

	testCases := []struct {
		name       string
		sessions   []profilerSession
		wantDelays []time.Duration
	}{
		{
			name: "a clean exit opens a new streak",
			sessions: []profilerSession{
				{waitErr: failure},
				{waitErr: failure},
				{},
				{waitErr: failure},
				{},
			},
			wantDelays: []time.Duration{time.Second, 2 * time.Second, time.Second},
		},
		{
			name: "a session that stayed up long enough opens a new streak",
			sessions: []profilerSession{
				{waitErr: failure},
				{waitErr: failure},
				{waitErr: failure, uptime: 31 * time.Second},
				{waitErr: failure},
				{},
			},
			wantDelays: []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			clock := newFakeClock()
			profiler := &fakeProfiler{t: t, clock: clock, sessions: tc.sessions}
			profiler.onStart = func(startCount int) {
				if startCount == len(tc.sessions) {
					cancel()
				}
			}

			policy := supervisor.RestartPolicy{Mode: supervisor.RestartAlways, Now: clock.Now, After: clock.After}
			err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), policy)

			require.NoError(t, err)
			require.Equal(t, len(tc.sessions), profiler.Starts())
			require.Equal(t, tc.wantDelays, clock.Delays())
		})
	}
}

func TestManageProfilerStopsWhenTheContextEndsDuringBackoff(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	shutdownCtx, shutdown := context.WithCancel(ctx)
	defer shutdown()

	clock := newBlockedClock()
	clock.onAfter = func(time.Duration) { shutdown() }

	profiler := &fakeProfiler{
		t:        t,
		clock:    clock,
		sessions: failingSessions(1, errors.New("cannot attach to pid 1234")),
	}

	policy := supervisor.RestartPolicy{Mode: supervisor.RestartAlways, Now: clock.Now, After: clock.After}
	err := supervisor.ManageProfiler(shutdownCtx, profiler, fakeParser{}, make(chan *collector.Sample, 1), policy)

	require.NoError(t, ctx.Err(), "ManageProfiler did not return within the test context")
	require.NoError(t, err, "a shutdown during the restart delay is not a failure")
	require.Equal(t, 1, profiler.Starts())
	require.Equal(t, []time.Duration{time.Second}, clock.Delays())
}

func TestManageProfilerDrainsStderrBeforeWaitingForTheProcess(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// The stream keeps writing after the parser is done, so a supervisor that reaps the process
	// first would be observed calling Wait with the reader still mid-stream.
	const trailingLines = 1024
	gate := make(chan struct{})
	clock := newBlockedClock()
	profiler := &fakeProfiler{t: t, clock: clock}
	profiler.sessions = []profilerSession{{
		stderr: &gatedReader{
			before: strings.NewReader(strings.Repeat("cannot attach to pid 1234\n", 8)),
			gate:   gate,
			after:  strings.NewReader(strings.Repeat("phpspy: read error, retrying\n", trailingLines)),
			onEOF:  profiler.markStderrEOF,
		},
	}}

	policy := supervisor.RestartPolicy{Mode: supervisor.RestartNo, Now: clock.Now, After: clock.After}
	err := supervisor.ManageProfiler(ctx, profiler, gateOpeningParser{gate: gate}, make(chan *collector.Sample, 1), policy)

	require.NoError(t, ctx.Err(), "ManageProfiler did not return within the test context")
	require.NoError(t, err)
	require.True(t, profiler.StderrDrained(), "the supervisor left profiler stderr unread")
	require.Equal(t, []bool{true}, profiler.WaitedAfterStderrEOF(), "the process was reaped before stderr was drained")
}

func TestManageProfilerReapsAfterTheStderrGrace(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// A grandchild that inherited the pipe holds stderr open forever; the gate is only released
	// so the reader goroutine can end with the test.
	gate := make(chan struct{})
	defer close(gate)

	clock := newFakeClock()
	profiler := &fakeProfiler{t: t, clock: clock}
	profiler.sessions = []profilerSession{{
		stderr: &gatedReader{
			before: strings.NewReader("cannot attach to pid 1234\n"),
			gate:   gate,
			after:  strings.NewReader(""),
			onEOF:  profiler.markStderrEOF,
		},
	}}

	policy := supervisor.RestartPolicy{Mode: supervisor.RestartNo, Now: clock.Now, After: clock.After}
	err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), policy)

	require.NoError(t, ctx.Err(), "ManageProfiler did not return while profiler stderr stayed open")
	require.NoError(t, err)
	require.Len(t, clock.Delays(), 1, "the reap was expected to wait out the stderr grace exactly once")
	require.Positive(t, clock.Delays()[0])
}

func TestManageProfilerEndsSessionOnUnreadableStdout(t *testing.T) {
	t.Parallel()

	const traceBlock = "0 func1 /app/helper.php:10\n1 main /app/index.php:1\n\n"
	overLongLine := traceBlock + strings.Repeat("x", fakeStdoutLineLimit*2) + "\n"

	testCases := []struct {
		name           string
		restart        string
		sessions       []profilerSession
		wantErr        error
		expectedRuns   int
		expectedTraces []string
	}{
		{
			name:    "no restart returns the read error",
			restart: supervisor.RestartNo,
			sessions: []profilerSession{
				{stdout: overLongLine, keepsRunning: true, waitErr: errors.New("signal: killed")},
			},
			wantErr:        bufio.ErrTooLong,
			expectedRuns:   1,
			expectedTraces: []string{"main;func1"},
		},
		{
			name:    "onerror restarts and a clean session ends the loop",
			restart: supervisor.RestartOnError,
			sessions: []profilerSession{
				{stdout: overLongLine, keepsRunning: true, waitErr: errors.New("signal: killed")},
				{stdout: traceBlock},
			},
			expectedRuns:   2,
			expectedTraces: []string{"main;func1", "main;func1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			clock := newFakeClock()
			profiler := &fakeProfiler{t: t, clock: clock, sessions: tc.sessions}
			samplesChannel := make(chan *collector.Sample, 10)
			parser := phpspy.NewParser(nil, nil, false, false)

			policy := supervisor.RestartPolicy{Mode: tc.restart, Now: clock.Now, After: clock.After}
			err := supervisor.ManageProfiler(ctx, profiler, parser, samplesChannel, policy)
			close(samplesChannel)

			require.NoError(t, ctx.Err(), "ManageProfiler did not return within the test context")
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expectedRuns, profiler.Starts())
			require.True(t, profiler.Terminated(), "profiler kept running after the read error")

			var traces []string
			for sample := range samplesChannel {
				traces = append(traces, sample.Trace)
			}
			require.Equal(t, tc.expectedTraces, traces)
		})
	}
}
