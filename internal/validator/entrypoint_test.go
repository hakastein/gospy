package validator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func setupValidator(patterns []string, cacheCapacity int) *EntryPointValidator {
	return NewEntryPointValidator(patterns, cacheCapacity)
}

func TestPHPEntryPointValidator(t *testing.T) {
	patterns := []string{
		"index.php",
		"/app/**/*.php",
		"api/*/v1/*",
	}
	validEntryPoints := []string{
		"index.php",
		"/app/controllers/test.php",
		"/public_html/code/index.php",
		"api/user/v1/info",
	}
	invalidEntryPoints := []string{
		"/app/main.js",
		"config.yaml",
		"/app/controllers/test.txt",
	}
	cacheCapacity := 100

	t.Run("ValidPHPEntryPoints", func(t *testing.T) {
		validator := setupValidator(patterns, cacheCapacity)
		for _, ep := range validEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.True(t, validator.IsValid(ep), "Expected entry point '%s' to be valid", ep)
			})
		}
	})

	t.Run("InvalidPHPEntryPoints", func(t *testing.T) {
		validator := setupValidator(patterns, cacheCapacity)
		for _, ep := range invalidEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.False(t, validator.IsValid(ep), "Expected entry point '%s' to be invalid", ep)
			})
		}
	})

	t.Run("EmptyPatterns", func(t *testing.T) {
		validator := setupValidator([]string{}, cacheCapacity)
		assert.True(t, validator.IsValid("any/entry/point"), "Any entry point should be valid when no patterns are defined")
	})

	t.Run("EmptyEntryPoint", func(t *testing.T) {
		validator := setupValidator(patterns, cacheCapacity)
		assert.False(t, validator.IsValid(""), "Empty entry point should be invalid")
	})

	t.Run("CacheUsage", func(t *testing.T) {
		validator := setupValidator(patterns, cacheCapacity)
		entryPoint := "index.php"

		// Check cache to confirm entry point was stored
		validator.mu.RLock()
		_, found := validator.cache.Get(entryPoint)
		validator.mu.RUnlock()
		assert.True(t, found, "Expected entry point '%s' to be cached", entryPoint)
	})
}
