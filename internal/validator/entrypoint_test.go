package validator_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/validator"
)

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

	t.Run("ValidPHPEntryPoints", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns)
		for _, ep := range validEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.True(t, v.IsValid(ep), "Expected entry point '%s' to be valid", ep)
			})
		}
	})

	t.Run("InvalidPHPEntryPoints", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns)
		for _, ep := range invalidEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.False(t, v.IsValid(ep), "Expected entry point '%s' to be invalid", ep)
			})
		}
	})

	t.Run("EmptyPatterns", func(t *testing.T) {
		t.Parallel()
		v := validator.New([]string{})
		assert.True(t, v.IsValid("any/entry/point"), "Any entry point should be valid when no patterns are defined")
	})

	t.Run("EmptyEntryPoint", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns)
		assert.False(t, v.IsValid(""), "Empty entry point should be invalid if patterns are defined")
	})

	t.Run("RepeatedChecksKeepTheirVerdict", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns)
		for range 2 {
			require.True(t, v.IsValid("index.php"))
			require.False(t, v.IsValid("/app/main.js"))
		}
	})
}
