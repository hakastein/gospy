package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"gospy/internal/args"
	"os/exec"
	"strings"
	"sync"
)

// Profiler implementation of profiler.Profiler
type Profiler struct {
	executable string
	args       []string
	cmd        *exec.Cmd
	mu         sync.Mutex
}

func NewProfiler(
	executable string,
	args []string,
) *Profiler {
	return &Profiler{
		executable: executable,
		args:       args,
	}
}

func (profiler *Profiler) Start(ctx context.Context) (*bufio.Scanner, error) {
	profiler.mu.Lock()
	defer profiler.mu.Unlock()

	cmd := exec.CommandContext(ctx, profiler.executable, profiler.args...)

	stdout, pipeError := cmd.StdoutPipe()
	if pipeError != nil {
		return nil, fmt.Errorf("stdout pipe error: %w", pipeError)
	}

	if startError := cmd.Start(); startError != nil {
		return nil, startError
	}

	profiler.cmd = cmd
	scanner := bufio.NewScanner(stdout)
	return scanner, nil
}

func (profiler *Profiler) Wait() error {
	profiler.mu.Lock()
	defer profiler.mu.Unlock()

	if profiler.cmd == nil {
		return errors.New("no command to wait for")
	}

	return profiler.cmd.Wait()
}

func (profiler *Profiler) IsConfigurationValid() (bool, error) {
	unsupportedFlags := []struct {
		longKey  string
		shortKey string
	}{
		{"version", "v"},
		{"top", "t"},
		{"help", "h"},
		{"single-line", "1"},
	}
	for _, keys := range unsupportedFlags {
		if args.ExtractFlagValue[bool](profiler.args, keys.longKey, keys.shortKey, false) {
			return false, fmt.Errorf("flag -%s/--%s is unsupported by gospy", keys.shortKey, keys.longKey)
		}
	}

	output := args.ExtractFlagValue[string](profiler.args, "output", "o", "stdout")
	if output != "stdout" && output != "-" {
		return false, errors.New("output must be set to stdout")
	}

	pgrepMode := args.ExtractFlagValue[string](profiler.args, "pgrep", "P", "")
	if pgrepMode != "" {
		bufferSize := args.ExtractFlagValue[int](profiler.args, "buffer-size", "b", 4096)
		eventHandlerOpts := args.ExtractFlagValue[string](profiler.args, "event-handler-opts", "J", "")
		if bufferSize > 4096 && !strings.Contains(eventHandlerOpts, "m") {
			log.Warn().Msg("using large buffer size without mutex; consider adding -J m with -b > 4096")
		}
	}
	return true, nil
}

func (profiler *Profiler) GetHZ() int {
	return args.ExtractFlagValue[int](profiler.args, "rate-hz", "H", 99)
}
