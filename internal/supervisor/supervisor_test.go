package supervisor_test

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/supervisor"
)

type fakeProfiler struct {
	t           *testing.T
	mu          sync.Mutex
	startCount  int
	waitCount   int
	stdout      string
	waitResults []error
	onStart     func(int)
}

func (p *fakeProfiler) Start(context.Context) (*bufio.Scanner, *bufio.Scanner, error) {
	p.mu.Lock()
	p.startCount++
	startCount := p.startCount
	onStart := p.onStart
	stdout := p.stdout
	p.mu.Unlock()

	if onStart != nil {
		onStart(startCount)
	}

	return bufio.NewScanner(strings.NewReader(stdout)), bufio.NewScanner(strings.NewReader("")), nil
}

func (p *fakeProfiler) Wait() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	require.Less(p.t, p.waitCount, len(p.waitResults), "unexpected Wait call")
	result := p.waitResults[p.waitCount]
	p.waitCount++
	return result
}

func (p *fakeProfiler) Starts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCount
}

type fakeParser struct{}

func (fakeParser) Parse(context.Context, *bufio.Scanner, chan<- *collector.Sample) {}

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
		waitResults   []error
		cancelOnStart int
		wantErr       error
		expectedRuns  int
	}{
		{
			name:         "graceful exit without restart",
			restart:      supervisor.RestartNo,
			waitResults:  []error{nil},
			expectedRuns: 1,
		},
		{
			name:         "wait error without restart",
			restart:      supervisor.RestartNo,
			waitResults:  []error{errors.New("profiler exited with error")},
			wantErr:      errors.New("profiler exited with error"),
			expectedRuns: 1,
		},
		{
			name:          "restart on success",
			restart:       supervisor.RestartOnSuccess,
			waitResults:   []error{nil, nil},
			cancelOnStart: 2,
			expectedRuns:  2,
		},
		{
			name:          "restart on error",
			restart:       supervisor.RestartOnError,
			waitResults:   []error{errors.New("first failure"), nil},
			cancelOnStart: 2,
			expectedRuns:  2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			profiler := &fakeProfiler{
				t:           t,
				waitResults: tc.waitResults,
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
