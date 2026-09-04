package phpspy

import (
	"errors"
	"strings"
	"unicode"
)

var (
	errEmptyTrace   = errors.New("empty trace")
	errFrameFormat  = errors.New("invalid trace format")
	errFrameFileLoc = errors.New("invalid file info in trace")
)

// phpspy prints frames innermost first as "index function path:line", so the entry point
// is the last line of the block. A single-frame block is a sample that landed in the entry
// script itself and folds to a one-node stack.
func foldTrace(trace []string, keepEntrypointName bool) (foldedStack string, entryPoint string, err error) {
	if len(trace) == 0 {
		return "", "", errEmptyTrace
	}

	lastIndex := len(trace) - 1
	entryFunction, entryLocation, err := splitFrame(trace[lastIndex])
	if err != nil {
		return "", "", err
	}

	colonIdx := strings.LastIndex(entryLocation, ":")
	if colonIdx == -1 {
		return "", "", errFrameFileLoc
	}
	entryPoint = entryLocation[:colonIdx]

	var stack strings.Builder
	stack.WriteString(entryFunction)
	if keepEntrypointName {
		stack.WriteString(" ")
		stack.WriteString(entryPoint)
	}

	for i := lastIndex - 1; i >= 0; i-- {
		function, _, frameErr := splitFrame(trace[i])
		if frameErr != nil {
			return "", "", frameErr
		}

		stack.WriteString(";")
		stack.WriteString(function)
	}

	return stack.String(), entryPoint, nil
}

// splitFrame cuts a frame line into its function name and its "path:line" location. Only the
// first two fields are split off: phpspy never puts a space in a function name, but a file path
// may well contain one, so everything after the function name is the location, verbatim.
func splitFrame(line string) (function string, location string, err error) {
	_, afterIndex, ok := cutField(strings.TrimSpace(line))
	if !ok {
		return "", "", errFrameFormat
	}

	function, location, ok = cutField(afterIndex)
	if !ok || location == "" {
		return "", "", errFrameFormat
	}

	return function, location, nil
}

// cutField splits off the leading whitespace-delimited field and returns the remainder with the
// separating whitespace removed.
func cutField(s string) (field string, rest string, ok bool) {
	idx := strings.IndexFunc(s, unicode.IsSpace)
	if idx == -1 {
		return "", "", false
	}

	return s[:idx], strings.TrimLeftFunc(s[idx:], unicode.IsSpace), true
}
