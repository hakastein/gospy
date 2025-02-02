package parser

import (
	"bufio"
	"context"
	"fmt"
	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
)

func Init(
	profiler string,
	entryPoints []string,
	tagsMapping map[string][]tag.DynamicTag,
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

type Parser interface {
	Parse(
		ctx context.Context,
		scanner *bufio.Scanner,
		samplesChannel chan<- *collector.Sample,
	)
}
