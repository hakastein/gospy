package parser

import (
	"fmt"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/types"
)

func Init(
	profiler string,
	entryPoints []string,
	tagsMapping map[string][]tag.DynamicTag,
	tagEntrypoint bool,
	keepEntrypointName bool,
) (types.Parser, error) {
	var parser types.Parser

	switch profiler {
	case "phpspy":
		parser = phpspy.NewParser(entryPoints, tagsMapping, tagEntrypoint, keepEntrypointName)
	default:
		return nil, fmt.Errorf("unknown profiler: %s", profiler)
	}

	return parser, nil
}
