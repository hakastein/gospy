package transform

import (
	"errors"
	"strings"
)

// TracesToFoldedStacks converts trace lines to folded stack format and extracts the entry point.
func TracesToFoldedStacks(trace []string, keepEntrypointName bool) (string, string, error) {
	if len(trace) < 2 {
		return "", "", errors.New("trace insufficient length")
	}

	var (
		foldedStack strings.Builder
		entryPoint  string
	)

	lastIndex := len(trace) - 1
	for i := lastIndex; i >= 0; i-- {
		tokens := strings.Fields(trace[i])
		// 0 - number of trace
		// 1 - function
		// 2 - path with line number
		if len(tokens) < 3 {
			return "", "", errors.New("invalid trace format")
		}

		foldedStack.WriteString(tokens[1])

		// Last line in trace is entry point
		if i == lastIndex {
			fileInfo := tokens[2]
			colonIdx := strings.LastIndex(fileInfo, ":")
			if colonIdx == -1 {
				return "", "", errors.New("invalid file info in trace")
			}
			entryPoint = fileInfo[:colonIdx]
			if keepEntrypointName {
				foldedStack.WriteString(" ")
				foldedStack.WriteString(entryPoint)
			}
		}

		if i > 0 {
			foldedStack.WriteString(";")
		}
	}

	return foldedStack.String(), entryPoint, nil
}
