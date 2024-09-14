package profiler

import (
	"bufio"
	"context"
	"fmt"
	"go.uber.org/zap"
	"gospy/internal/phpspy"
	"gospy/internal/sample"
	"time"
)

// Profiler interface
type Profiler interface {
	Start(ctx context.Context) (*bufio.Scanner, error)
	Stop() error
	Wait() error
	ParseOutput(
		ctx context.Context,
		cancel context.CancelFunc,
		scanner *bufio.Scanner,
		samplesChannel chan<- *sample.Collection,
	)
}

func Run(
	profilerApp string,
	profilerArguments []string,
	logger *zap.Logger,
	accumulationInterval time.Duration,
	entryPoints map[string]struct{},
	dynamicTags map[string]string,
) (Profiler, error) {
	var profiler Profiler
	var profilerErr error
	switch profilerApp {
	case "phpspy":
		profiler, profilerErr = phpspy.NewProfiler(profilerApp, profilerArguments, logger, accumulationInterval, entryPoints, dynamicTags)
		if profilerErr != nil {
			return nil, profilerErr
		}
	default:
		return nil, fmt.Errorf("unsupported profiler: %s", profilerApp)
	}

	return profiler, nil
}
