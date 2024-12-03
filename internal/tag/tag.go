package tag

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// isTagKeyRuneAllowed checks if a rune is allowed in tag keys and values.
// This function is a copy of isTagKeyRuneAllowed from pyroscope/pkg/og/flameql/flameql.go
func isTagKeyRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' ||
		r == '.'
}

// DynamicTag represents a dynamic tag with optional regex and replacement.
type DynamicTag struct {
	TagKey     string
	TagRegexp  *regexp.Regexp
	TagReplace string
}

func (t DynamicTag) GetValue(input string) string {
	if t.TagRegexp != nil && t.TagReplace != "" {
		input = t.TagRegexp.ReplaceAllString(input, t.TagReplace)
	}

	// replace coma to greek coma, it's nasty, but it's work
	input = strings.ReplaceAll(input, ",", "͵")

	return input
}

// ParseInput processes input tags and separates static and dynamic tags.
// Returns a string of static tags, a map of dynamic tags, and an error if necessary.
func ParseInput(tagsInput []string) (string, map[string]DynamicTag, error) {
	dynamicTags := make(map[string]DynamicTag)
	var staticTags []string

	sort.Strings(tagsInput)

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

		if len(value) >= 4 && strings.HasPrefix(value, "{{") && strings.HasSuffix(value, "}}") {
			inner := strings.TrimSpace(value[2 : len(value)-2])
			parts, err := parseQuotedStrings(inner)
			if err != nil {
				return "", nil, fmt.Errorf("invalid dynamic tag format in `%s`: %v", tag, err)
			}

			switch len(parts) {
			case 1:
				dynamicTags[parts[0]] = DynamicTag{
					TagKey: key,
				}
			case 3:
				regex, rerr := regexp.Compile(parts[1])
				if rerr != nil {
					return "", nil, fmt.Errorf("invalid regex `%s` in tag `%s`: %v", parts[1], tag, rerr)
				}
				dynamicTags[parts[0]] = DynamicTag{
					TagKey:     key,
					TagRegexp:  regex,
					TagReplace: parts[2],
				}
			default:
				return "", nil, fmt.Errorf("unexpected number of parameters in dynamic tag `%s`", tag)
			}
		} else {
			if strings.ContainsRune(value, ',') {
				return "", nil, fmt.Errorf("invalid tag value `%s`, can't use comma symbol", value)
			}
			staticTags = append(staticTags, tag)
		}
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}

// parseQuotedStrings parses a string containing exactly three or one quoted substrings.
// Example input: `"key" "regex" "$1"`
func parseQuotedStrings(input string) ([]string, error) {
	var parts []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	for i, r := range input {
		if escaped {
			// Add the escaped character and reset the escaped flag
			current.WriteRune(r)
			escaped = false
			continue
		}

		switch r {
		case '\\':
			if inQuotes {
				escaped = true
			} else {
				// Backslashes outside quotes are treated as literal characters
				current.WriteRune(r)
			}
		case '"':
			inQuotes = !inQuotes
			if !inQuotes {
				parts = append(parts, current.String())
				current.Reset()
			}
		case ' ', '\t', '\r', '\n':
			if inQuotes {
				current.WriteRune(r)
			} else {
				if current.Len() > 0 {
					// Unexpected non-quoted character
					return nil, fmt.Errorf("unexpected space at position %d", i)
				}
				// Ignore multiple spaces between quotes
			}
		default:
			if inQuotes {
				current.WriteRune(r)
			} else {
				return nil, fmt.Errorf("unexpected character '%c' at position %d", r, i)
			}
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("unterminated quote in input")
	}

	if len(parts) != 3 && len(parts) != 1 {
		return nil, fmt.Errorf("expected exactly 3 or 1 quoted strings, got %d", len(parts))
	}

	return parts, nil
}

func isValidTagKey(s string) bool {
	for _, r := range s {
		if !isTagKeyRuneAllowed(r) {
			return false
		}
	}
	return true
}
