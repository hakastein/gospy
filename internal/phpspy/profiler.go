package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

const (
	defaultBufferSize = 4096
	defaultRateHz     = 99
)

var (
	unsupportedFlags = []flag{
		{long: "version", short: "v"},
		{long: "top", short: "t"},
		{long: "help", short: "h"},
		{long: "single-line", short: "1"},
	}
	outputFlag           = flag{long: "output", short: "o"}
	pgrepFlag            = flag{long: "pgrep", short: "P"}
	bufferSizeFlag       = flag{long: "buffer-size", short: "b"}
	eventHandlerOptsFlag = flag{long: "event-handler-opts", short: "J"}
	rateHzFlag           = flag{long: "rate-hz", short: "H"}
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

func (profiler *Profiler) Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error) {
	profiler.mu.Lock()
	defer profiler.mu.Unlock()

	cmd := exec.CommandContext(ctx, profiler.executable, profiler.args...)
	log.Debug().
		Str("executable", profiler.executable).
		Strs("args", profiler.args).
		Msg("launching profiler process")

	stdout, pipeError := cmd.StdoutPipe()
	if pipeError != nil {
		return nil, nil, fmt.Errorf("stdout pipe error: %w", pipeError)
	}

	stderr, pipeError := cmd.StderrPipe()
	if pipeError != nil {
		return nil, nil, fmt.Errorf("stderr pipe error: %w", pipeError)
	}

	if startError := cmd.Start(); startError != nil {
		return nil, nil, startError
	}

	profiler.cmd = cmd
	return bufio.NewScanner(stdout), bufio.NewScanner(stderr), nil
}

func (profiler *Profiler) Wait() error {
	profiler.mu.Lock()
	defer profiler.mu.Unlock()

	if profiler.cmd == nil {
		return errors.New("no command to wait for")
	}

	return profiler.cmd.Wait()
}

func (profiler *Profiler) ValidateConfiguration() error {
	for _, unsupported := range unsupportedFlags {
		if unsupported.enabled(profiler.args) {
			return fmt.Errorf("flag -%s/--%s is unsupported by gospy", unsupported.short, unsupported.long)
		}
	}

	if output := outputFlag.text(profiler.args, "stdout"); output != "stdout" && output != "-" {
		return errors.New("output must be set to stdout")
	}

	if pgrepFlag.text(profiler.args, "") != "" {
		bufferSize := bufferSizeFlag.number(profiler.args, defaultBufferSize)
		eventHandlerOpts := eventHandlerOptsFlag.text(profiler.args, "")
		if bufferSize > defaultBufferSize && !strings.Contains(eventHandlerOpts, "m") {
			log.Warn().Msg("using large buffer size without mutex; consider adding -J m with -b > 4096")
		}
	}

	return nil
}

func (profiler *Profiler) GetHZ() int {
	return rateHzFlag.number(profiler.args, defaultRateHz)
}
