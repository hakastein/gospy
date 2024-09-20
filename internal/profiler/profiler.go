package profiler

import (
	"bufio"
	"context"
	"fmt"
	"gospy/internal/phpspy"
	"path/filepath"
)

type Args struct {
	RateHz int
}

// Profiler interface
type Profiler interface {
	Start(ctx context.Context) (*bufio.Scanner, error)
	Wait() error
	IsConfigurationValid() (bool, error)
	GetHZ() int
}

func Init(
	profilerPath string,
	profilerArguments []string,
) (Profiler, error) {
	var profiler Profiler

	switch filepath.Base(profilerPath) {
	case "phpspy":
		profiler = phpspy.NewProfiler(profilerPath, profilerArguments)
	default:
		return nil, fmt.Errorf("unsupported profiler: %s", profilerPath)
	}

	return profiler, nil
}
