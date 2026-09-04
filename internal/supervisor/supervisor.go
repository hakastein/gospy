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

// ManageProfiler reports nil for a context that ended: a shutdown is not a failed run.
func ManageProfiler(
	ctx context.Context,
	profilerInstance profilerRunner,
	parserInstance traceParser,
	foldedStacksChannel chan *collector.Sample,
	policy RestartPolicy,
) error {
	policy = policy.withDefaults()

	failures := 0
	delay := policy.BaseDelay

	for {
		if ctx.Err() != nil {
			return nil
		}

		log.Info().Msg("starting profiler")
		startedAt := policy.Now()
		session := runSession(ctx, profilerInstance, parserInstance, foldedStacksChannel)
		uptime := policy.Now().Sub(startedAt)

		if session.startFailed {
			log.Error().Err(session.err).Msg("error starting profiler")
			return session.err
		}

		logSessionEnd(ctx, session)

		if ctx.Err() != nil {
			return nil
		}

		if !policy.restartAllowed(session.err) {
			return session.err
		}

		if session.err == nil {
			failures, delay = 0, policy.BaseDelay
			continue
		}

		if uptime >= healthySession {
			failures, delay = 0, policy.BaseDelay
		}

		failures++
		if policy.exhausted(failures) {
			return fmt.Errorf("profiler failed %d times in a row, giving up: %w", policy.MaxConsecutiveFailures, session.err)
		}

		log.Warn().
			Int("failures", failures).
			Dur("delay", delay).
			Msg("waiting before restarting the profiler")

		if !policy.pause(ctx, delay) {
			return nil
		}
		delay = policy.nextDelay(delay)
	}
}

func logSessionEnd(ctx context.Context, session sessionResult) {
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
