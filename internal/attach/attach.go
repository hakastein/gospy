// Package attach runs one phpspy per process: `phpspy -p <pid>` with its own pipes, its
// stdout parsed into samples, its stderr logged, and its end classified for the planner.
package attach

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
)

// phpspy prints frames above bufio's 64 KiB default through eval'd paths, deep vendor trees
// and large peeked globals; a line over the cap fails the attach instead of being parsed.
const (
	maxStdoutLineSize       = 1 << 20
	initialStdoutBufferSize = 64 << 10
)

// phpspy has no signal handlers and dies at SIGTERM; the grace covers a process stuck in the
// kernel and a helper that inherited the pipes and outlived it.
const terminateGrace = 2 * time.Second

// DefaultSilenceTimeout is how long an attach may print nothing while phpspy keeps reporting
// that it cannot read the process before gospy gives up on it. phpspy never exits on EPERM by
// itself; it prints a failure per sample instead, so a denied attach is never short of them.
const DefaultSilenceTimeout = 15 * time.Second

// phpspy's message for a read of the target's memory that failed.
const memoryReadFailure = "copy_proc_mem"

// phpspy is chatty on stderr under load, but the handful of lines explaining a failed attach
// must always get through.
const stderrBurst = 20

// Config describes one attach.
type Config struct {
	Executable string
	PID        int
	// Rate is phpspy's -H value in Hz; every Sample carries it too, whatever Parser says.
	Rate   int
	Args   []string
	Parser phpspy.ParserConfig
	// Logger receives phpspy's stderr at warn level; the caller stamps it with the target and PID.
	Logger zerolog.Logger
	// SilenceTimeout at or below zero takes DefaultSilenceTimeout.
	SilenceTimeout time.Duration
}

// Outcome is how an attach ended.
type Outcome uint8

const (
	// Exited means phpspy returned 0: the process it traced ended.
	Exited Outcome = iota
	// Failed means phpspy returned non-zero, its output could not be read, or it sat silent
	// while reporting that it could not read the process.
	Failed
	// Detached means Detach was called, or the context ended, before phpspy was done.
	Detached
)

func (outcome Outcome) String() string {
	switch outcome {
	case Exited:
		return "exited"
	case Failed:
		return "failed"
	case Detached:
		return "detached"
	default:
		return "unknown"
	}
}

// Result is the end of an attach; Err explains a Failed outcome.
type Result struct {
	Outcome Outcome
	Err     error
}

// Attach is one running phpspy.
type Attach struct {
	cfg        Config
	cmd        *exec.Cmd
	stdout     *os.File
	stderr     *os.File
	endSession context.CancelFunc
	detached   atomic.Bool
	lastOutput atomic.Int64
	lastDenied atomic.Int64
	silenced   atomic.Bool
	exited     chan struct{}
	waitErr    error
	done       chan struct{}
	result     Result
}

// Start launches `<executable> -p <pid> -H <rate> <args…>` and returns once phpspy is running.
// An error means phpspy could not be started at all: a missing or unusable binary, a failed
// fork, or a ctx that had already ended. Cancelling ctx abandons the attach: phpspy is
// signalled and the parser stops at once, unlike Detach, which lets the parser drain what
// phpspy already wrote.
func Start(ctx context.Context, cfg Config, samples chan<- *collector.Sample) (*Attach, error) {
	if cfg.SilenceTimeout <= 0 {
		cfg.SilenceTimeout = DefaultSilenceTimeout
	}
	cfg.Parser.SampleRate = cfg.Rate

	sessionCtx, endSession := context.WithCancel(ctx)

	args := append([]string{"-p", strconv.Itoa(cfg.PID), "-H", strconv.Itoa(cfg.Rate)}, cfg.Args...)
	cmd := exec.CommandContext(sessionCtx, cfg.Executable, args...)
	cmd.SysProcAttr = sessionAttributes()
	cmd.Cancel = func() error {
		return signalGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = terminateGrace

	cfg.Logger.Debug().Str("executable", cfg.Executable).Strs("args", args).Msg("starting phpspy")

	// Own pipes instead of os/exec's: Cmd.Wait closes those, losing phpspy's last output after it exits.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		endSession()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		endSession()
		closeAll(stdout, stdoutWriter)
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	startErr := cmd.Start()
	// os/exec leaves a caller's files open, and a write end kept here would hold off EOF for good.
	closeAll(stdoutWriter, stderrWriter)
	if startErr != nil {
		endSession()
		closeAll(stdout, stderr)
		return nil, fmt.Errorf("cannot start %s: %w", cfg.Executable, startErr)
	}

	attach := &Attach{
		cfg:        cfg,
		cmd:        cmd,
		stdout:     stdout,
		stderr:     stderr,
		endSession: endSession,
		exited:     make(chan struct{}),
		done:       make(chan struct{}),
	}
	attach.lastOutput.Store(time.Now().UnixNano())

	go attach.reap()
	go attach.run(ctx, samples)

	return attach, nil
}

// Detach ends the attach: phpspy gets SIGTERM and the parser reads what is left in the pipe.
func (attach *Attach) Detach() {
	attach.detached.Store(true)
	attach.endSession()
}

// Done closes once Wait has a Result.
func (attach *Attach) Done() <-chan struct{} {
	return attach.done
}

// Wait blocks until phpspy is gone and its output is parsed.
func (attach *Attach) Wait() Result {
	<-attach.done

	return attach.result
}

// The helpers phpspy shells out to while it attaches can outlive it holding the pipes, so
// the group is always swept once phpspy itself is gone.
func (attach *Attach) reap() {
	defer close(attach.exited)

	attach.waitErr = attach.cmd.Wait()

	if err := signalGroup(attach.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		attach.cfg.Logger.Warn().Err(err).Msg("cannot kill the processes phpspy left behind")
	}
}

func (attach *Attach) run(ctx context.Context, samples chan<- *collector.Sample) {
	defer close(attach.done)

	stderrDone := attach.consumeStderr()

	var parseErr error
	parsed := make(chan struct{})
	go func() {
		defer close(parsed)

		scanner := bufio.NewScanner(&activityReader{reader: attach.stdout, last: &attach.lastOutput})
		scanner.Buffer(make([]byte, 0, initialStdoutBufferSize), maxStdoutLineSize)
		parseErr = phpspy.NewParser(attach.cfg.Parser).Parse(ctx, scanner, samples)
	}()

	watchdogDone := make(chan struct{})
	go attach.watch(watchdogDone)

	select {
	case <-parsed:
		// Only a read error ends the parser while phpspy lives, and then phpspy sits blocked on
		// a pipe nobody reads any more.
		if parseErr != nil {
			attach.endSession()
		}
	case <-attach.exited:
	}

	<-attach.exited
	close(watchdogDone)

	// The group is swept, so the pipes report EOF unless a helper that escaped it holds them.
	if !closedWithin(terminateGrace, parsed, stderrDone) {
		attach.cfg.Logger.Debug().Dur("grace", terminateGrace).Msg("phpspy output still open after its exit, closing it")
		closeAll(attach.stdout, attach.stderr)
	}
	<-parsed
	<-stderrDone
	closeAll(attach.stdout, attach.stderr)

	attach.result = attach.classify(ctx, parseErr)
	attach.cfg.Logger.Debug().
		Str("outcome", attach.result.Outcome.String()).
		AnErr("error", attach.result.Err).
		Msg("phpspy ended")
}

// closedWithin reports whether every channel closed before the grace ran out.
func closedWithin(grace time.Duration, channels ...<-chan struct{}) bool {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()

	for _, channel := range channels {
		select {
		case <-channel:
		case <-deadline.C:
			return false
		}
	}

	return true
}

func (attach *Attach) consumeStderr() <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		logger := attach.cfg.Logger.Sample(&zerolog.BurstSampler{Burst: stderrBurst, Period: time.Second})
		scanner := bufio.NewScanner(attach.stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, memoryReadFailure) {
				attach.lastDenied.Store(time.Now().UnixNano())
			}

			logger.Warn().Str("line", line).Msg("phpspy stderr")
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			attach.cfg.Logger.Debug().Err(err).Msg("error reading phpspy stderr")
		}
	}()

	return done
}

// watch kills an attach that prints no trace block for SilenceTimeout while its stderr keeps
// reporting failed memory reads, with the latest one in the second half of that silence:
// phpspy on a process it may not trace. An attach that is merely quiet, an idle worker with
// no errors or with one transient error behind it, is left alone.
func (attach *Attach) watch(done <-chan struct{}) {
	timeout := attach.cfg.SilenceTimeout.Nanoseconds()

	ticker := time.NewTicker(min(attach.cfg.SilenceTimeout/2, time.Second))
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			silence := now.UnixNano() - attach.lastOutput.Load()
			sinceDenied := now.UnixNano() - attach.lastDenied.Load()
			if silence < timeout || sinceDenied > timeout/2 {
				continue
			}

			attach.silenced.Store(true)
			attach.endSession()

			return
		}
	}
}

func (attach *Attach) classify(ctx context.Context, parseErr error) Result {
	switch {
	case attach.silenced.Load():
		return Result{
			Outcome: Failed,
			Err:     fmt.Errorf("no trace block for %s while phpspy kept reporting %s failures", attach.cfg.SilenceTimeout, memoryReadFailure),
		}
	case parseErr != nil && !errors.Is(parseErr, os.ErrClosed):
		return Result{Outcome: Failed, Err: fmt.Errorf("cannot read phpspy output: %w", parseErr)}
	case attach.detached.Load() || ctx.Err() != nil:
		return Result{Outcome: Detached}
	case attach.waitErr == nil:
		return Result{Outcome: Exited}
	default:
		return Result{Outcome: Failed, Err: attach.waitErr}
	}
}

// activityReader records when phpspy last wrote anything, which is what the silence watchdog
// measures: in the case it hunts, phpspy writes nothing at all to stdout.
type activityReader struct {
	reader io.Reader
	last   *atomic.Int64
}

func (reader *activityReader) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	if read > 0 {
		reader.last.Store(time.Now().UnixNano())
	}

	return read, err
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

// signalGroup reports os.ErrProcessDone once nothing is left in the group, as exec.Cmd.Cancel expects.
func signalGroup(leader int, signal syscall.Signal) error {
	err := syscall.Kill(-leader, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	return err
}
