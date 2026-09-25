package supervisor_test

import (
	"bufio"
	"context"
	"errors"
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

// profilerSession scripts one run of the fake profiler. keepsRunning holds Wait until the
// session context is cancelled, the way a profiler blocked writing into an unread pipe does.
type profilerSession struct {
	stdout       string
	waitErr      error
	keepsRunning bool
}

type fakeProfiler struct {
	t          *testing.T
	mu         sync.Mutex
	sessions   []profilerSession
	startCount int
	waitCount  int
	sessionCtx context.Context
	terminated bool
	onStart    func(int)
}

func (p *fakeProfiler) Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error) {
	p.mu.Lock()
	require.Less(p.t, p.startCount, len(p.sessions), "unexpected Start call")
	session := p.sessions[p.startCount]
	p.startCount++
	startCount := p.startCount
	p.sessionCtx = ctx
	onStart := p.onStart
	p.mu.Unlock()

	if onStart != nil {
		onStart(startCount)
	}

	stdout := bufio.NewScanner(strings.NewReader(session.stdout))
	stdout.Buffer(make([]byte, 0, fakeStdoutLineLimit), fakeStdoutLineLimit)

	return stdout, bufio.NewScanner(strings.NewReader("")), nil
}

func (p *fakeProfiler) Wait() error {
	p.mu.Lock()
	require.Less(p.t, p.waitCount, len(p.sessions), "unexpected Wait call")
	session := p.sessions[p.waitCount]
	p.waitCount++
	sessionCtx := p.sessionCtx
	p.mu.Unlock()

	if !session.keepsRunning {
		return session.waitErr
	}

	<-sessionCtx.Done()

	p.mu.Lock()
	p.terminated = true
	p.mu.Unlock()

	return session.waitErr
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

type fakeParser struct{}

func (fakeParser) Parse(context.Context, *bufio.Scanner, chan<- *collector.Sample) error {
	return nil
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

			profiler := &fakeProfiler{
				t:        t,
				sessions: tc.sessions,
				onStart: func(startCount int) {
					if tc.cancelOnStart != 0 && startCount == tc.cancelOnStart {
						cancel()
					}
				},
			}

			err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), tc.restart)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr.Error())
			}
			require.Equal(t, tc.expectedRuns, profiler.Starts())
		})
	}
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

			profiler := &fakeProfiler{t: t, sessions: tc.sessions}
			samplesChannel := make(chan *collector.Sample, 10)
			parser := phpspy.NewParser(nil, nil, false, false)

			err := supervisor.ManageProfiler(ctx, profiler, parser, samplesChannel, tc.restart)
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
