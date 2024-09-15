package profiler

import (
	"bufio"
	"context"
	"fmt"
	"gospy/internal/phpspy"
	"gospy/internal/sample"
	"path/filepath"
	"time"
)

// Profiler interface
type Profiler interface {
	Start(ctx context.Context) (*bufio.Scanner, error)
	Stop() error
	Wait() error
	ParseOutput(
		ctx context.Context,
		scanner *bufio.Scanner,
		samplesChannel chan<- *sample.Collection,
	)
}

func Run(
	profilerPath string,
	profilerArguments []string,
	accumulationInterval time.Duration,
	entryPoints map[string]struct{},
	dynamicTags map[string]string,
) (Profiler, error) {
	var profiler Profiler
	var profilerErr error

	switch filepath.Base(profilerPath) {
	case "phpspy":
		profiler, profilerErr = phpspy.NewProfiler(profilerPath, profilerArguments, accumulationInterval, entryPoints, dynamicTags)
		if profilerErr != nil {
			return nil, profilerErr
		}
	default:
		return nil, fmt.Errorf("unsupported profiler: %s", profilerPath)
	}

	return profiler, nil
}
