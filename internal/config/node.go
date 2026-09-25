package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// node wraps a YAML node with the path it was reached by, so that an error can say which key
// of which target is wrong.
type node struct {
	*yaml.Node
	path string
}

type keyValue struct {
	key   string
	value node
}

func (n node) errorf(format string, args ...any) error {
	where := n.path
	if where == "" {
		where = "document"
	}

	return fmt.Errorf("line %d: %s: %s", n.Line, where, fmt.Sprintf(format, args...))
}

func (n node) child(key string) string {
	if n.path == "" {
		return key
	}

	return n.path + "." + key
}

func (n node) deref() node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n.Node = n.Alias
	}

	return n
}

func (n node) isNull() bool {
	n = n.deref()

	return n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

// mapping returns the pairs of a mapping in file order, rejecting a key given twice.
func (n node) mapping() ([]keyValue, error) {
	n = n.deref()
	if n.isNull() {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, n.errorf("expected a mapping of keys to values")
	}

	pairs := make([]keyValue, 0, len(n.Content)/2)
	seen := make(map[string]struct{}, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if _, duplicate := seen[key]; duplicate {
			return nil, node{Node: n.Content[i], path: n.child(key)}.errorf("key given twice")
		}
		seen[key] = struct{}{}

		pairs = append(pairs, keyValue{key: key, value: node{Node: n.Content[i+1], path: n.child(key)}})
	}

	return pairs, nil
}

func (n node) sequence() ([]node, error) {
	n = n.deref()
	if n.isNull() {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, n.errorf("expected a list")
	}

	items := make([]node, 0, len(n.Content))
	for i, item := range n.Content {
		items = append(items, node{Node: item, path: fmt.Sprintf("%s[%d]", n.path, i)})
	}

	return items, nil
}

// scalar returns the text of a scalar, whatever type YAML would have given it: the schema
// decides the type, not the quoting.
func (n node) scalar() (string, error) {
	n = n.deref()
	if n.Kind != yaml.ScalarNode {
		return "", n.errorf("expected a single value")
	}

	return n.Value, nil
}

func (n node) text() (string, error) {
	if n.isNull() {
		return "", nil
	}

	return n.scalar()
}

func (n node) integer() (int, error) {
	text, err := n.scalar()
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, n.errorf("expected an integer, got %q", text)
	}

	return value, nil
}

func (n node) number() (float64, error) {
	text, err := n.scalar()
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, n.errorf("expected a number, got %q", text)
	}

	return value, nil
}

func (n node) boolean() (bool, error) {
	text, err := n.scalar()
	if err != nil {
		return false, err
	}

	switch strings.TrimSpace(text) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, n.errorf("expected true or false, got %q", text)
	}
}

// duration reads Go syntax; a bare number other than 0 has no unit and is rejected, as is a
// negative value.
func (n node) duration() (time.Duration, error) {
	text, err := n.scalar()
	if err != nil {
		return 0, err
	}

	value, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil {
		return 0, n.errorf("expected a duration such as 10s or 500ms, got %q", text)
	}
	if value < 0 {
		return 0, n.errorf("expected a duration of zero or more, got %q", text)
	}

	return value, nil
}

func (n node) strings() ([]string, error) {
	items, err := n.sequence()
	if err != nil {
		return nil, err
	}

	values := make([]string, 0, len(items))
	for _, item := range items {
		value, err := item.scalar()
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

// expandValues runs the environment expansion over every scalar value below n, keys
// excluded. A dynamic tag under a tags key is left as written: its rewrite names capture
// groups as $1 and ${name}, which are the regexp engine's to read, not the environment's.
func expandValues(n *yaml.Node, lookup LookupFunc) error {
	switch n.Kind {
	case yaml.ScalarNode:
		expanded, err := expand(n.Value, lookup)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		n.Value = expanded
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			value := n.Content[i]
			if n.Content[i-1].Value == "tags" && value.Kind == yaml.MappingNode {
				if err := expandTags(value, lookup); err != nil {
					return err
				}
				continue
			}

			if err := expandValues(value, lookup); err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range n.Content {
			if err := expandValues(child, lookup); err != nil {
				return err
			}
		}
	}

	return nil
}

func expandTags(tags *yaml.Node, lookup LookupFunc) error {
	for i := 1; i < len(tags.Content); i += 2 {
		value := tags.Content[i]
		if value.Kind == yaml.ScalarNode && strings.HasPrefix(strings.TrimSpace(value.Value), "{{") {
			continue
		}

		if err := expandValues(value, lookup); err != nil {
			return err
		}
	}

	return nil
}
