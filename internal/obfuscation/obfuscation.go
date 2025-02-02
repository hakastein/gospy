package obfuscation

import (
	"strings"
	"unicode/utf8"
)

// MaskString masks a string by replacing characters with '*'.
// It preserves the first 'startKeep' and last 'endKeep' runes.
// If the string length is less than or equal to startKeep + endKeep,
// it returns a string of '*' with the same length.
func MaskString(s string, options ...int) string {
	const defaultStartKeep = 1
	const defaultEndKeep = 1

	startKeep, endKeep := defaultStartKeep, defaultEndKeep
	if len(options) >= 1 {
		startKeep = options[0]
	}
	if len(options) >= 2 {
		endKeep = options[1]
	}

	runeCount := utf8.RuneCountInString(s)
	if runeCount <= startKeep+endKeep {
		return strings.Repeat("*", runeCount)
	}

	var builder strings.Builder
	builder.Grow(len(s))

	current := 0
	for _, r := range s {
		if current < startKeep || current >= runeCount-endKeep {
			builder.WriteRune(r)
		} else {
			builder.WriteRune('*')
		}
		current++
	}

	return builder.String()
}
