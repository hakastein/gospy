package tag

import (
	"fmt"
	"strings"
)

// IsTagKeyRuneAllowed checks if a rune is allowed in tag keys and values,
// this function is copy of IsTagKeyRuneAllowed from pyroscope/pkg/og/flameql/flameql.go
func IsTagKeyRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' ||
		r == '.'
}

// ParseInput processes input tags and separates static and dynamic tags
func ParseInput(tagsInput []string) (string, map[string]string, error) {
	dynamicTags := make(map[string]string)
	var staticTags []string

	for _, tag := range tagsInput {
		idx := strings.Index(tag, "=")
		if idx == -1 {
			return "", nil, fmt.Errorf("unexpected tag value `%s`, expected format is tag=value", tag)
		}

		key := tag[:idx]
		value := tag[idx+1:]

		if !isValidTagKey(key) {
			return "", nil, fmt.Errorf("invalid tag key `%s`", key)
		}

		if len(value) > 2 && value[0] == '%' && value[len(value)-1] == '%' {
			dynamicTags[value[1:len(value)-1]] = key
		} else {
			if strings.ContainsRune(value, ',') {
				return "", nil, fmt.Errorf("invalid tag value `%s`, can't use coma symbol", value)
			}
			staticTags = append(staticTags, tag)
		}
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}

func isValidTagKey(s string) bool {
	for _, r := range s {
		if !IsTagKeyRuneAllowed(r) {
			return false
		}
	}
	return true
}