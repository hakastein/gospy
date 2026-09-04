package tag

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	dynamicTagPrefix = "{{"
	dynamicTagSuffix = "}}"

	// commaReplacement stands in for a comma, which would otherwise start a new label.
	// The Greek lower numeral sign looks like a comma and carries no meaning in the grammar.
	commaReplacement = '͵'
	// reservedReplacement stands in for every other character the grammar reserves.
	reservedReplacement = '_'
)

// isTagKeyRuneAllowed checks if a rune is allowed in tag keys.
// This function is a copy of isTagKeyRuneAllowed from pyroscope/pkg/og/flameql/flameql.go
func isTagKeyRuneAllowed(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' ||
		r == '.'
}

// isReservedValueRune reports whether a rune would corrupt Pyroscope's `app{key=value,...}`
// application name: braces delimit the label set, `=` separates key from value, `,` separates
// labels, and quotes and blanks are not part of the grammar at all.
func isReservedValueRune(r rune) bool {
	switch r {
	case '{', '}', '=', ',', '"':
		return true
	}

	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// SanitizeValue makes a tag value safe to embed in Pyroscope's `app{key=value,...}` application
// name. A comma becomes the Greek lower numeral sign; every other reserved character — `{`, `}`,
// `=`, `"`, whitespace and control characters — becomes an underscore. Replacement is
// one-to-one, so the value keeps its length and stays readable in the UI.
func SanitizeValue(value string) string {
	if !strings.ContainsFunc(value, isReservedValueRune) {
		return value
	}

	var sanitized strings.Builder
	sanitized.Grow(len(value))

	for _, r := range value {
		switch {
		case r == ',':
			sanitized.WriteRune(commaReplacement)
		case isReservedValueRune(r):
			sanitized.WriteRune(reservedReplacement)
		default:
			sanitized.WriteRune(r)
		}
	}

	return sanitized.String()
}

// DynamicTag represents a dynamic tag with optional regex and replacement.
type DynamicTag struct {
	TagKey     string
	TagRegexp  *regexp.Regexp
	TagReplace string
}

// GetValue applies the optional regex rewrite and sanitizes the result. An empty replacement is
// honoured: the match is removed.
func (t DynamicTag) GetValue(input string) string {
	if t.TagRegexp != nil {
		input = t.TagRegexp.ReplaceAllString(input, t.TagReplace)
	}

	return SanitizeValue(input)
}

// ParseInput processes input tags and separates static and dynamic tags.
// Returns a string of static tags, a map of dynamic tags, and an error if necessary.
func ParseInput(tagsInput []string) (string, map[string][]DynamicTag, error) {
	dynamicTags := make(map[string][]DynamicTag)
	var staticTags []string

	sort.Strings(tagsInput)

	for _, tagInput := range tagsInput {
		idx := strings.Index(tagInput, "=")
		if idx == -1 {
			return "", nil, fmt.Errorf("unexpected tag value `%s`, expected format is tag=value", tagInput)
		}

		key := strings.TrimSpace(tagInput[:idx])
		value := strings.TrimSpace(tagInput[idx+1:])

		if !isValidTagKey(key) {
			return "", nil, fmt.Errorf("invalid tag key `%s`", key)
		}

		if !strings.HasPrefix(value, dynamicTagPrefix) {
			if strings.ContainsFunc(value, isReservedValueRune) {
				return "", nil, fmt.Errorf(
					"invalid value of tag `%s`: `{`, `}`, `=`, `,`, `\"`, whitespace and control characters are not allowed",
					key,
				)
			}

			staticTags = append(staticTags, key+"="+value)

			continue
		}

		parts, err := parseDynamicTag(value)
		if err != nil {
			return "", nil, fmt.Errorf("invalid dynamic tag `%s`: %w", key, err)
		}

		metaKey := parts[0]
		dynamicTag := DynamicTag{TagKey: key}

		if len(parts) == 3 {
			regex, rerr := regexp.Compile(parts[1])
			if rerr != nil {
				return "", nil, fmt.Errorf("invalid regex `%s` in tag `%s`: %w", parts[1], key, rerr)
			}
			dynamicTag.TagRegexp = regex
			dynamicTag.TagReplace = parts[2]
		}

		dynamicTags[metaKey] = append(dynamicTags[metaKey], dynamicTag)
	}

	return strings.Join(staticTags, ","), dynamicTags, nil
}

// parseDynamicTag unwraps a `{{ ... }}` value. A value that opens with `{{` is always meant to be
// dynamic, so anything that fails to parse is an error rather than a static tag with literal
// braces.
func parseDynamicTag(value string) ([]string, error) {
	if len(value) < len(dynamicTagPrefix)+len(dynamicTagSuffix) || !strings.HasSuffix(value, dynamicTagSuffix) {
		return nil, errors.New("missing closing `}}`")
	}

	inner := strings.TrimSpace(value[len(dynamicTagPrefix) : len(value)-len(dynamicTagSuffix)])

	return parseQuotedStrings(inner)
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
			current.WriteRune(r)
			escaped = false
			continue
		}

		switch r {
		case '\\':
			if inQuotes {
				escaped = true
			} else {
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
					return nil, fmt.Errorf("unexpected space at position %d", i)
				}
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
		return nil, errors.New("unterminated quote in input")
	}

	if len(parts) != 3 && len(parts) != 1 {
		return nil, fmt.Errorf("expected exactly 3 or 1 quoted strings, got %d", len(parts))
	}

	return parts, nil
}

func isValidTagKey(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if !isTagKeyRuneAllowed(r) {
			return false
		}
	}

	return true
}
