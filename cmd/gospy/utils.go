package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maskString masks a string by replacing characters with '*'.
// It preserves the first 'startKeep' and last 'endKeep' runes.
// If the string length is less than or equal to startKeep + endKeep,
// it returns a string of '*' with the same length.
func maskString(s string, options ...int) string {
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

// parseTags processes input tags and separates static and dynamic tags
func parseTags(tagsInput []string) (string, map[string]string, error) {
	dynamicTags := make(map[string]string)
	var staticTags []string

	for _, tag := range tagsInput {
		idx := strings.Index(tag, "=")
		if idx != -1 {
			key := tag[:idx]
			value := tag[idx+1:]
			if len(value) > 1 && value[0] == '%' && value[len(value)-1] == '%' {
				dynamicTags[value[1:len(value)-1]] = key
			} else {
				staticTags = append(staticTags, tag)
			}
		} else {
			return "", nil, fmt.Errorf("unexpected tag format %s, expected tag=value", tag)
		}
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}
