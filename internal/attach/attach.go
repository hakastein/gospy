// Package attach runs one `phpspy -p <pid>` per process and classifies its end for the planner.
package attach

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	maxLineSize       = 1 << 20
	initialBufferSize = 64 << 10
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
	// Counters are shared by the attaches of one target; nil counts nowhere.
	Counters *Counters
}

type Counters struct {
	PartialTraces  atomic.Int64
	FilteredTraces atomic.Int64
}

// Outcome is how an attach ended.
type Outcome uint8

const (
	// Exited means phpspy returned 0: the process it traced ended.
	Exited Outcome = iota
	// Failed means phpspy returned non-zero, its output could not be read, or it reported that
	// it could not read the process while producing nothing.
	Failed
	// Detached means Detach was called, or the context ended, before phpspy was done.
	Detached
)

// String names the outcome for logs.
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
	cfg            Config
	cmd            *exec.Cmd
	output         *os.File
	stderrLog      zerolog.Logger
	endAttach      context.CancelFunc
	detached       atomic.Bool
	lastOutput     atomic.Int64
	denied         atomic.Int64
	deniedAtOutput atomic.Int64
	silenced       atomic.Bool
	exited         chan struct{}
	waitErr        error
	done           chan struct{}
	result         Result
}

// Start launches `<executable> -p <pid> -H <rate> <args…>` and returns once phpspy is running.
// An error means phpspy could not be started at all: a missing or unusable binary, a failed
// fork, or a ctx that had already ended. Cancelling ctx abandons the attach: phpspy is
// signaled and the parser stops at once, unlike Detach, which lets the parser drain what
// phpspy already wrote.
func Start(ctx context.Context, cfg Config, samples chan<- *collector.Sample) (*Attach, error) {
	if cfg.SilenceTimeout <= 0 {
		cfg.SilenceTimeout = DefaultSilenceTimeout
	}
	if cfg.Counters == nil {
		cfg.Counters = &Counters{}
	}
	cfg.Parser.SampleRate = cfg.Rate

	attachCtx, endAttach := context.WithCancel(ctx)

	args := append([]string{"-p", strconv.Itoa(cfg.PID), "-H", strconv.Itoa(cfg.Rate)}, phpspy.WithDefaults(cfg.Args)...)
	cmd := exec.CommandContext(attachCtx, cfg.Executable, args...)
	cmd.SysProcAttr = processAttributes()
	cmd.Cancel = func() error {
		return signalGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = terminateGrace

	cfg.Logger.Debug().Str("executable", cfg.Executable).Strs("args", args).Msg("starting phpspy")

	// Own pipe instead of os/exec's: Cmd.Wait closes those, losing phpspy's last output after it
	// exits. stdout and stderr share it, since the parser ties diagnostics to blocks by order.
	output, outputWriter, err := os.Pipe()
	if err != nil {
		endAttach()
		return nil, fmt.Errorf("output pipe: %w", err)
	}

	cmd.Stdout, cmd.Stderr = outputWriter, outputWriter
	startErr := cmd.Start()
	// os/exec leaves a caller's files open, and a write end kept here would hold off EOF for good.
	closeAll(outputWriter)
	if startErr != nil {
		endAttach()
		closeAll(output)
		return nil, fmt.Errorf("cannot start %s: %w", cfg.Executable, startErr)
	}

	attach := &Attach{
		cfg:       cfg,
		cmd:       cmd,
		output:    output,
		stderrLog: cfg.Logger.Sample(&zerolog.BurstSampler{Burst: stderrBurst, Period: time.Second}),
		endAttach: endAttach,
		exited:    make(chan struct{}),
		done:      make(chan struct{}),
	}
	attach.lastOutput.Store(time.Now().UnixNano())

	go attach.reap()
	go attach.run(ctx, samples)

	return attach, nil
}

// Detach ends the attach: phpspy gets SIGTERM and the parser reads what is left in the pipe.
func (attach *Attach) Detach() {
	attach.detached.Store(true)
	attach.endAttach()
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
	// The context stays registered with its parent until it is cancelled, however phpspy ended.
	defer attach.endAttach()
	defer closeAll(attach.output)

	var parseErr error
	parsed := make(chan struct{})
	go func() {
		defer close(parsed)

		scanner := bufio.NewScanner(attach.output)
		scanner.Buffer(make([]byte, 0, initialBufferSize), maxLineSize)
		parseErr = phpspy.NewParser(attach.cfg.Parser, observer{attach: attach}).Parse(ctx, scanner, samples)
	}()

	watchdogDone := make(chan struct{})
	go attach.watch(watchdogDone)

	select {
	case <-parsed:
		// Only a read error ends the parser while phpspy lives, and then phpspy sits blocked on
		// a pipe nobody reads any more.
		if parseErr != nil {
			attach.endAttach()
		}
	case <-attach.exited:
	}

	<-attach.exited
	close(watchdogDone)

	// The group is swept, so the pipe reports EOF unless a helper that escaped it holds it.
	if !closedWithin(terminateGrace, parsed) {
		attach.cfg.Logger.Debug().Dur("grace", terminateGrace).Msg("phpspy output still open after its exit, closing it")
		closeAll(attach.output)
	}
	<-parsed

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

// deniedSinceOutput is how many memory-read failures phpspy reported after its last trace
// block; a second's worth of samples without a block is a process it may not read.
func (attach *Attach) deniedSinceOutput() int64 {
	return attach.denied.Load() - attach.deniedAtOutput.Load()
}

func (attach *Attach) deniedThreshold() int64 {
	return int64(max(attach.cfg.Rate, 1))
}

// watch kills an attach that prints no trace block for SilenceTimeout while its stderr keeps
// reporting failed memory reads: phpspy on a process it may not trace, which never exits by
// itself. An attach that is merely quiet, an idle worker with no errors or with a transient
// error behind it, is left alone.
func (attach *Attach) watch(done <-chan struct{}) {
	timeout := attach.cfg.SilenceTimeout.Nanoseconds()

	ticker := time.NewTicker(min(attach.cfg.SilenceTimeout/2, time.Second))
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			if now.UnixNano()-attach.lastOutput.Load() < timeout || attach.deniedSinceOutput() < attach.deniedThreshold() {
				continue
			}

			attach.silenced.Store(true)
			attach.endAttach()

			return
		}
	}
}

// classify reads the end of an attach. Denial evidence outranks the exit: an attach rotated
// or pre-empted before the watchdog could act is still a failed one.
func (attach *Attach) classify(ctx context.Context, parseErr error) Result {
	switch {
	case attach.silenced.Load():
		return Result{
			Outcome: Failed,
			Err:     fmt.Errorf("no trace block for %s while phpspy kept reporting %s failures", attach.cfg.SilenceTimeout, memoryReadFailure),
		}
	case parseErr != nil && !errors.Is(parseErr, os.ErrClosed):
		return Result{Outcome: Failed, Err: fmt.Errorf("cannot read phpspy output: %w", parseErr)}
	case attach.deniedSinceOutput() >= attach.deniedThreshold():
		return Result{
			Outcome: Failed,
			Err:     fmt.Errorf("phpspy reported %d %s failures and no trace block since", attach.deniedSinceOutput(), memoryReadFailure),
		}
	case attach.detached.Load() || ctx.Err() != nil:
		return Result{Outcome: Detached}
	case attach.waitErr == nil:
		return Result{Outcome: Exited}
	default:
		return Result{Outcome: Failed, Err: attach.waitErr}
	}
}

// Every trace block, dropped ones included, resets the silence watchdog.
type observer struct {
	attach *Attach
}

func (observer observer) Diagnostic(line string) {
	if strings.Contains(line, memoryReadFailure) {
		observer.attach.denied.Add(1)
	}

	observer.attach.stderrLog.Warn().Str("line", line).Msg("phpspy stderr")
}

func (observer observer) Block(outcome phpspy.BlockOutcome) {
	attach := observer.attach
	attach.deniedAtOutput.Store(attach.denied.Load())
	attach.lastOutput.Store(time.Now().UnixNano())

	switch outcome {
	case phpspy.Partial:
		attach.cfg.Counters.PartialTraces.Add(1)
	case phpspy.Filtered:
		attach.cfg.Counters.FilteredTraces.Add(1)
	case phpspy.Sampled, phpspy.Malformed:
	}
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
