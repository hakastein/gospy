package supervisor

import (
	"context"
	"github.com/rs/zerolog/log"
	"gospy/internal/parser"
	"gospy/internal/profiler"
)

func ManageProfiler(
	ctx context.Context,
	profilerInstance profiler.Profiler,
	parserInstance parser.Parser,
	foldedStacksChannel chan [2]string,
	restart string,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			log.Info().Msg("starting profiler")
			scanner, err := profilerInstance.Start(ctx)

			if err != nil {
				log.Error().Err(err).Msg("error starting profiler")
				return
			}

			parserInstance.Parse(ctx, scanner, foldedStacksChannel)

			err = profilerInstance.Wait()
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
				return
			}

			switch {
			case restart == "always":
				continue
			case restart == "onerror" && err != nil:
				continue
			case restart == "onsuccess" && err == nil:
				continue
			default:
				return
			}
		}
	}
}
