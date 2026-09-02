package phpspy

import (
	"errors"
	"strings"
)

// phpspy prints frames innermost first as "index function path:line", so the entry point
// is the last line of the block.
func foldTrace(trace []string, keepEntrypointName bool) (foldedStack string, entryPoint string, err error) {
	if len(trace) < 2 {
		return "", "", errors.New("trace insufficient length")
	}

	lastIndex := len(trace) - 1
	entryFrame := strings.Fields(trace[lastIndex])
	if len(entryFrame) < 3 {
		return "", "", errors.New("invalid trace format")
	}

	colonIdx := strings.LastIndex(entryFrame[2], ":")
	if colonIdx == -1 {
		return "", "", errors.New("invalid file info in trace")
	}
	entryPoint = entryFrame[2][:colonIdx]

	var stack strings.Builder
	stack.WriteString(entryFrame[1])
	if keepEntrypointName {
		stack.WriteString(" ")
		stack.WriteString(entryPoint)
	}

	for i := lastIndex - 1; i >= 0; i-- {
		frame := strings.Fields(trace[i])
		if len(frame) < 3 {
			return "", "", errors.New("invalid trace format")
		}

		stack.WriteString(";")
		stack.WriteString(frame[1])
	}

	return stack.String(), entryPoint, nil
}
