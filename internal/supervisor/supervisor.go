package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
)

type profilerRunner interface {
	Start(ctx context.Context) (*bufio.Scanner, *bufio.Scanner, error)
	Wait() error
}

type traceParser interface {
	Parse(ctx context.Context, scanner *bufio.Scanner, samplesChannel chan<- *collector.Sample) error
}

// sessionResult reports how one profiler session ended. err drives the restart policy;
// startFailed skips it entirely, and readFailed marks a session gospy killed itself.
type sessionResult struct {
	err         error
	startFailed bool
	readFailed  bool
}

// Restart policies, spelled as the --restart flag takes them.
const (
	RestartNo        = "no"
	RestartAlways    = "always"
	RestartOnError   = "onerror"
	RestartOnSuccess = "onsuccess"
)

// ValidateRestart reports whether the text names a policy the supervisor knows how to apply.
func ValidateRestart(restart string) error {
	switch restart {
	case RestartNo, RestartAlways, RestartOnError, RestartOnSuccess:
		return nil
	default:
		return fmt.Errorf("invalid restart option: %s", restart)
	}
}

// shouldRestart reports whether a session that ended with err starts again; anything the
// supervisor does not know, the unset value included, keeps the profiler stopped.
func shouldRestart(restart string, err error) bool {
	switch restart {
	case RestartAlways:
		return true
	case RestartOnError:
		return err != nil
	case RestartOnSuccess:
		return err == nil
	default:
		return false
	}
}

// ManageProfiler run profiler and parser, collect parses, transform parses into folded stacks format, send to foldedStacksChannel
func ManageProfiler(
	ctx context.Context,
	profilerInstance profilerRunner,
	parserInstance traceParser,
	foldedStacksChannel chan *collector.Sample,
	restart string,
) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		log.Info().Msg("starting profiler")
		session := runSession(ctx, profilerInstance, parserInstance, foldedStacksChannel)

		if session.startFailed {
			log.Error().Err(session.err).Msg("error starting profiler")
			return session.err
		}

		switch {
		case session.err == nil:
			log.Info().Msg("profiler exited gracefully")
		case session.readFailed:
			log.Error().Err(session.err).Msg("profiler stopped: cannot read its stdout")
		case ctx.Err() != nil:
			log.Info().Msg("profiler terminated")
		default:
			log.Error().Err(session.err).Msg("profiler exited with error")
		}

		if ctx.Err() != nil {
			return nil
		}

		if shouldRestart(restart, session.err) {
			continue
		}

		return session.err
	}
}

func runSession(
	ctx context.Context,
	profilerInstance profilerRunner,
	parserInstance traceParser,
	foldedStacksChannel chan *collector.Sample,
) sessionResult {
	sessionCtx, endSession := context.WithCancel(ctx)
	defer endSession()

	stdoutScanner, stderrScanner, startErr := profilerInstance.Start(sessionCtx)
	if startErr != nil {
		return sessionResult{err: startErr, startFailed: true}
	}

	stderrDone := consumeProfilerStderr(stderrScanner)
	log.Debug().Msg("profiler started, waiting for samples from stdout")

	readErr := parserInstance.Parse(sessionCtx, stdoutScanner, foldedStacksChannel)
	if readErr != nil {
		// The profiler stays alive blocked on writing into a pipe nobody reads any more, so
		// waiting on it before killing it would never return.
		endSession()
	}

	log.Debug().Msg("parser finished, waiting for profiler exit")
	waitErr := profilerInstance.Wait()
	<-stderrDone

	if readErr != nil {
		return sessionResult{err: readErr, readFailed: true}
	}

	return sessionResult{err: waitErr}
}

func consumeProfilerStderr(stderrScanner *bufio.Scanner) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if stderrScanner == nil {
			return
		}

		stderrLogger := log.Sample(&zerolog.BurstSampler{
			Burst:  1,
			Period: time.Second,
		})
		for stderrScanner.Scan() {
			stderrLogger.Trace().Str("line", stderrScanner.Text()).Msg("profiler stderr")
		}
		if err := stderrScanner.Err(); err != nil {
			log.Debug().Err(err).Msg("error reading profiler stderr")
		}
	}()

	return done
}
