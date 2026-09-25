package config

import (
	"fmt"
	"strings"
)

// LookupFunc reads one environment variable, os.LookupEnv style.
type LookupFunc func(name string) (string, bool)

// expand replaces ${VAR} and ${VAR:-default} in text. A reference to a variable that is not
// set, or set but empty, takes its default; without one it is an error naming the variable.
// $$ is a literal dollar sign; a dollar sign followed by anything else is kept as written,
// so the $1 of a tag rewrite survives.
func expand(text string, lookup LookupFunc) (string, error) {
	if !strings.Contains(text, "$") {
		return text, nil
	}

	var expanded strings.Builder
	expanded.Grow(len(text))

	for i := 0; i < len(text); i++ {
		if text[i] != '$' || i+1 >= len(text) {
			expanded.WriteByte(text[i])
			continue
		}

		switch text[i+1] {
		case '$':
			expanded.WriteByte('$')
			i++
		case '{':
			end := strings.IndexByte(text[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated reference %q", text[i:])
			}

			value, err := resolve(text[i+2:i+end], lookup)
			if err != nil {
				return "", err
			}

			expanded.WriteString(value)
			i += end
		default:
			expanded.WriteByte('$')
		}
	}

	return expanded.String(), nil
}

func resolve(reference string, lookup LookupFunc) (string, error) {
	name, fallback, hasFallback := strings.Cut(reference, ":-")
	if !validName(name) {
		return "", fmt.Errorf("malformed reference ${%s}", reference)
	}

	if value, ok := lookup(name); ok && (value != "" || !hasFallback) {
		return value, nil
	}

	if hasFallback {
		return fallback, nil
	}

	return "", fmt.Errorf("environment variable %s is not set and has no default", name)
}

func validName(name string) bool {
	if name == "" {
		return false
	}

	for i, r := range name {
		letter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		digit := r >= '0' && r <= '9'
		if !letter && !(digit && i > 0) {
			return false
		}
	}

	return true
}
