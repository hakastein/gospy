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
	Parse(ctx context.Context, scanner *bufio.Scanner, samplesChannel chan<- *collector.Sample)
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
		stdoutScanner, stderrScanner, err := profilerInstance.Start(ctx)

		if err != nil {
			log.Error().Err(err).Msg("error starting profiler")
			return err
		}

		stderrDone := consumeProfilerStderr(stderrScanner)
		log.Debug().Msg("profiler started, waiting for samples from stdout")
		parserInstance.Parse(ctx, stdoutScanner, foldedStacksChannel)
		log.Debug().Msg("parser finished, waiting for profiler exit")

		err = profilerInstance.Wait()
		<-stderrDone
		if err != nil {
			if ctx.Err() != nil {
				log.Info().Msg("profiler terminated")
			} else {
				log.Error().Err(err).Msg("profiler exited with error")
			}
		} else {
			log.Info().Msg("profiler exited gracefully")
		}

		if ctx.Err() != nil {
			return nil
		}

		if shouldRestart(restart, err) {
			continue
		}

		return err
	}
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
