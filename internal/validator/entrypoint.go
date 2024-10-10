package validator

import (
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
	lru "github.com/hashicorp/golang-lru"
)

// EntryPointValidator validates entry points against predefined patterns with caching.
type EntryPointValidator struct {
	patterns []string
	cache    *lru.Cache
	mu       sync.RWMutex
}

// hasWildcard checks if the pattern contains any wildcard characters.
func hasWildcard(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// NewEntryPointValidator creates a new EntryPointValidator with the given patterns and cache capacity.
// It panics if the cache cannot be created.
func NewEntryPointValidator(patterns []string, cacheCapacity int) *EntryPointValidator {
	cache, err := lru.New(cacheCapacity)
	if err != nil {
		panic("failed to create LRU cache: " + err.Error())
	}

	return &EntryPointValidator{
		patterns: patterns,
		cache:    cache,
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
// It utilizes an LRU cache to store and retrieve validation results.
func (v *EntryPointValidator) IsValid(entryPoint string) bool {
	if len(v.patterns) == 0 {
		return true
	}

	v.mu.RLock()
	if cached, found := v.cache.Get(entryPoint); found {
		v.mu.RUnlock()
		return cached.(bool)
	}
	v.mu.RUnlock()

	// Perform pattern matching.
	isValid := false
	for _, pattern := range v.patterns {
		if matches(entryPoint, pattern) {
			isValid = true
			break
		}
	}

	// Store the result in cache with write lock.
	v.mu.Lock()
	v.cache.Add(entryPoint, isValid)
	v.mu.Unlock()

	return isValid
}
