package validator

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	cacheSize = 1000
	noTTL     = 0
)

// EntryPointValidator validates entry points against predefined patterns with caching.
type EntryPointValidator struct {
	patterns []string
	cache    *expirable.LRU[string, bool]
}

// hasWildcard checks if the pattern contains any wildcard characters.
func hasWildcard(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func New(patterns []string) *EntryPointValidator {
	return &EntryPointValidator{
		patterns: patterns,
		cache:    expirable.NewLRU[string, bool](cacheSize, nil, noTTL),
	}
}

// matches checks if the entryPoint matches the given pattern.
func matches(entryPoint, pattern string) bool {
	if hasWildcard(pattern) {
		match, err := doublestar.Match(pattern, entryPoint)
		return err == nil && match
	}
	return entryPoint == pattern || strings.HasSuffix(entryPoint, "/"+pattern)
}

// IsValid determines if the entryPoint is valid based on the patterns.
func (v *EntryPointValidator) IsValid(entryPoint string) bool {
	if len(v.patterns) == 0 {
		return true
	}

	if cached, found := v.cache.Get(entryPoint); found {
		return cached
	}

	isValid := false
	for _, pattern := range v.patterns {
		if matches(entryPoint, pattern) {
			isValid = true
			break
		}
	}

	v.cache.Add(entryPoint, isValid)

	return isValid
}
