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

func Init(
	profiler string,
	entryPoints []string,
	tagsMapping map[string]string,
	tagEntrypoint bool,
	keepEntrypointName bool,
) (Parser, error) {
	var parser Parser

	switch profiler {
	case "phpspy":
		parser = phpspy.NewParser(entryPoints, tagsMapping, tagEntrypoint, keepEntrypointName)
	default:
		return nil, fmt.Errorf("unknown profiler: %s", profiler)
	}

	return parser, nil
}
