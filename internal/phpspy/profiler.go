package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// phpspy prints frames above bufio's 64 KiB default through eval'd paths, deep vendor trees
// and large peeked globals; a line over the cap ends the session instead of being parsed.
const (
	maxStdoutLineSize       = 1 << 20
	initialStdoutBufferSize = 64 << 10
)

const terminateGrace = 2 * time.Second

// Modes that make phpspy print something other than a stream of trace blocks on stdout.
var unsupportedModes = []option{
	{long: "version", short: "v"},
	{long: "top", short: "t"},
	{long: "help", short: "h"},
	{long: "single-line", short: "1"},
}

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
	cmd.SysProcAttr = sessionAttributes()
	cmd.Cancel = func() error {
		return signalGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = terminateGrace
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

	stdoutScanner := bufio.NewScanner(stdout)
	stdoutScanner.Buffer(make([]byte, 0, initialStdoutBufferSize), maxStdoutLineSize)

	return stdoutScanner, bufio.NewScanner(stderr), nil
}

func (profiler *Profiler) Wait() error {
	profiler.mu.Lock()
	defer profiler.mu.Unlock()

	if profiler.cmd == nil {
		return errors.New("no command to wait for")
	}

	waitErr := profiler.cmd.Wait()

	// The group outlives its leader while phpspy's own children are left in it.
	if killErr := signalGroup(profiler.cmd.Process.Pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		log.Warn().Err(killErr).Msg("cannot kill processes the profiler left behind")
	}

	return waitErr
}

// signalGroup reports os.ErrProcessDone once nothing is left in the group, as exec.Cmd.Cancel expects.
func signalGroup(leader int, signal syscall.Signal) error {
	err := syscall.Kill(-leader, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	return err
}

func (profiler *Profiler) ValidateConfiguration() error {
	args := parseArgs(profiler.args)

	for _, unsupported := range unsupportedModes {
		if args.present(unsupported.long) {
			return fmt.Errorf("flag -%s/--%s is unsupported by gospy", unsupported.short, unsupported.long)
		}
	}

	if output := args.text(optionOutput, stdoutPath); output != stdoutPath {
		return fmt.Errorf("phpspy must write to stdout: pass `-o %s` or omit the flag, got %q", stdoutPath, output)
	}

	if handler := args.text(optionEventHandler, foutHandler); handler != foutHandler {
		return fmt.Errorf("event handler %q is unsupported by gospy, expected %s", handler, foutHandler)
	}

	if profiler.GetHZ() < 1 {
		return fmt.Errorf("sleep interval %s ns is longer than one second: Pyroscope needs a sample rate of at least 1 Hz", args.text(optionSleepNs, ""))
	}

	if args.present(optionPgrep) {
		bufferSize := args.number(optionBufferSize, defaultBufferSize)
		if bufferSize > pipeBufSize && !strings.Contains(args.text(optionEventHandlerOpts, ""), "m") {
			log.Warn().
				Int("buffer_size", bufferSize).
				Int("pipe_buf", pipeBufSize).
				Msg("buffer above PIPE_BUF without a mutex interlaces writes in pgrep mode; add -J m")
		}
	}

	return nil
}

// phpspy routes --rate-hz and --sleep-ns to the same interval.
func (profiler *Profiler) GetHZ() int {
	rate := defaultRateHz

	for _, given := range parseArgs(profiler.args) {
		switch given.long {
		case optionRateHz:
			if hz, err := strconv.Atoi(given.value); err == nil && hz > 0 {
				rate = hz
			}
		case optionSleepNs:
			if sleep, err := strconv.Atoi(given.value); err == nil && sleep > 0 {
				rate = nanosecondsPerSecond / sleep
			}
		}
	}

	return rate
}
