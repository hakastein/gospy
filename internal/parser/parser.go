package parser

import (
	"fmt"
	"gospy/internal/phpspy"
	"gospy/internal/types"
)

func Init(
	profiler string,
	entryPoints []string,
	tagsMapping map[string]string,
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
