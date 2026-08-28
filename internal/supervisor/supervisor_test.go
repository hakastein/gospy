package supervisor_test

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/supervisor"
	"github.com/stretchr/testify/require"
)

type fakeProfiler struct {
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

	result := p.waitResults[p.waitCount]
	p.waitCount++
	return result
}

func (p *fakeProfiler) Starts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startCount
}

type fakeParser struct {
	parseFunc func(context.Context, *bufio.Scanner, chan<- *collector.Sample)
}

func (p fakeParser) Parse(ctx context.Context, scanner *bufio.Scanner, samples chan<- *collector.Sample) {
	if p.parseFunc != nil {
		p.parseFunc(ctx, scanner, samples)
	}
}

func TestManageProfilerReturnsNilAfterGracefulExitWithoutRestart(t *testing.T) {
	profiler := &fakeProfiler{
		stdout:      "trace line\n",
		waitResults: []error{nil},
	}

	parsed := make(chan *collector.Sample, 1)
	parser := fakeParser{
		parseFunc: func(_ context.Context, scanner *bufio.Scanner, samples chan<- *collector.Sample) {
			require.True(t, scanner.Scan())
			samples <- &collector.Sample{Trace: scanner.Text()}
		},
	}

	err := supervisor.ManageProfiler(context.Background(), profiler, parser, parsed, "no")
	require.NoError(t, err)
	require.Equal(t, 1, profiler.Starts())

	sample := <-parsed
	require.Equal(t, "trace line", sample.Trace)
}

func TestManageProfilerReturnsWaitErrorWithoutRestart(t *testing.T) {
	waitErr := errors.New("profiler exited with error")
	profiler := &fakeProfiler{
		waitResults: []error{waitErr},
	}

	err := supervisor.ManageProfiler(context.Background(), profiler, fakeParser{}, make(chan *collector.Sample, 1), "no")
	require.ErrorIs(t, err, waitErr)
	require.Equal(t, 1, profiler.Starts())
}

func TestManageProfilerRestartsOnSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	profiler := &fakeProfiler{
		waitResults: []error{nil, nil},
		onStart: func(startCount int) {
			if startCount == 2 {
				cancel()
			}
		},
	}

	err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), "onsuccess")
	require.NoError(t, err)
	require.Equal(t, 2, profiler.Starts())
}

func TestManageProfilerRestartsOnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	profiler := &fakeProfiler{
		waitResults: []error{errors.New("first failure"), nil},
		onStart: func(startCount int) {
			if startCount == 2 {
				cancel()
			}
		},
	}

	err := supervisor.ManageProfiler(ctx, profiler, fakeParser{}, make(chan *collector.Sample, 1), "onerror")
	require.NoError(t, err)
	require.Equal(t, 2, profiler.Starts())
}
