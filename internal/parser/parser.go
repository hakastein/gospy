package parser

import (
	"bufio"
	"context"
	"fmt"
	"gospy/internal/phpspy"
)

type Parser interface {
	Parse(
		ctx context.Context,
		scanner *bufio.Scanner,
		samplesChannel chan<- [2]string,
	)
}

func Get(
	profiler string,
	entryPoints map[string]struct{},
	tagsMapping map[string]string,
) (Parser, error) {
	var parser Parser

	switch profiler {
	case "phpspy":
		parser = phpspy.NewParser(entryPoints, tagsMapping)
	default:
		return nil, fmt.Errorf("unknown profiler: %s", profiler)
	}

	return parser, nil
}
