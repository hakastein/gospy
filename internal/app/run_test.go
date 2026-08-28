package app

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/parser"
	"github.com/hakastein/gospy/internal/pyroscope"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/stretchr/testify/require"
)

type fakeProfiler struct {
	scanner  *bufio.Scanner
	startErr error
	waitErr  error
}

func (f *fakeProfiler) Start(ctx context.Context) (*bufio.Scanner, error) {
	return f.scanner, f.startErr
}

func (f *fakeProfiler) Wait() error {
	return f.waitErr
}

func (f *fakeProfiler) IsConfigurationValid() (bool, error) {
	return true, nil
}

func (f *fakeProfiler) GetHZ() int {
	return 99
}

type fakeParser struct{}

func (f *fakeParser) Parse(ctx context.Context, scanner *bufio.Scanner, samples chan<- *collector.Sample) {
	for scanner.Scan() {
	}
}

func TestRunStopsAfterProfilerExit(t *testing.T) {
	cfg := Config{
		PyroscopeURL:     "http://pyroscope.test",
		PyroscopeWorkers: 0,
		Restart:          "no",
		ProfilerApp:      "phpspy",
		StatsInterval:    time.Second,
	}

	deps := dependencies{
		newProfiler: func(profilerApp string, profilerArguments []string) (profilerInstance, error) {
			return &fakeProfiler{
				scanner: bufio.NewScanner(strings.NewReader("")),
			}, nil
		},
		newParser: func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error) {
			return &fakeParser{}, nil
		},
		newClient: func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client {
			return pyroscope.NewClient(pyroscopeURL, pyroscopeAuth, nil)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(ctx, cfg, deps)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return after profiler exit")
	}
}

func TestRunAcceptsProfilerPath(t *testing.T) {
	cfg := Config{
		PyroscopeURL:     "http://pyroscope.test",
		PyroscopeWorkers: 0,
		Restart:          "no",
		ProfilerApp:      "/usr/bin/phpspy",
		StatsInterval:    time.Second,
	}

	deps := dependencies{
		newProfiler: func(profilerApp string, profilerArguments []string) (profilerInstance, error) {
			return &fakeProfiler{
				scanner: bufio.NewScanner(strings.NewReader("")),
			}, nil
		},
		newParser: func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error) {
			return parser.Init(profilerApp, entryPoints, tagsMapping, tagEntrypoint, keepEntrypointName)
		},
		newClient: func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client {
			return pyroscope.NewClient(pyroscopeURL, pyroscopeAuth, nil)
		},
	}

	err := runWithDependencies(context.Background(), cfg, deps)
	require.NoError(t, err)
}

func TestRunReturnsProfilerStartError(t *testing.T) {
	cfg := Config{
		PyroscopeURL:     "http://pyroscope.test",
		PyroscopeWorkers: 0,
		Restart:          "no",
		ProfilerApp:      "phpspy",
		StatsInterval:    time.Second,
	}

	startErr := errors.New("start failed")
	deps := dependencies{
		newProfiler: func(profilerApp string, profilerArguments []string) (profilerInstance, error) {
			return &fakeProfiler{startErr: startErr}, nil
		},
		newParser: func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error) {
			return &fakeParser{}, nil
		},
		newClient: func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client {
			return pyroscope.NewClient(pyroscopeURL, pyroscopeAuth, nil)
		},
	}

	err := runWithDependencies(context.Background(), cfg, deps)
	require.ErrorIs(t, err, startErr)
}

func TestRunReturnsProfilerWaitError(t *testing.T) {
	cfg := Config{
		PyroscopeURL:     "http://pyroscope.test",
		PyroscopeWorkers: 0,
		Restart:          "no",
		ProfilerApp:      "phpspy",
		StatsInterval:    time.Second,
	}

	waitErr := errors.New("wait failed")
	deps := dependencies{
		newProfiler: func(profilerApp string, profilerArguments []string) (profilerInstance, error) {
			return &fakeProfiler{
				scanner: bufio.NewScanner(strings.NewReader("")),
				waitErr: waitErr,
			}, nil
		},
		newParser: func(profilerApp string, entryPoints []string, tagsMapping map[string][]tag.DynamicTag, tagEntrypoint bool, keepEntrypointName bool) (parserInstance, error) {
			return &fakeParser{}, nil
		},
		newClient: func(pyroscopeURL string, pyroscopeAuth string, pyroscopeTimeout time.Duration) *pyroscope.Client {
			return pyroscope.NewClient(pyroscopeURL, pyroscopeAuth, nil)
		},
	}

	err := runWithDependencies(context.Background(), cfg, deps)
	require.ErrorIs(t, err, waitErr)
}
