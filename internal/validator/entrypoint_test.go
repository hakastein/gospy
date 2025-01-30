package validator_test

import (
	"github.com/hakastein/gospy/internal/validator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
)

type CacheMock struct {
	mock.Mock
}

func (m *CacheMock) Get(key interface{}) (interface{}, bool) {
	args := m.Called(key)
	return args.Get(0), args.Bool(1)
}

func (m *CacheMock) Add(key, value interface{}) bool {
	args := m.Called(key, value)
	return args.Bool(0)
}

func NewCacheMock() *CacheMock {
	cacheMock := new(CacheMock)
	cacheMock.On("Add", mock.AnythingOfType("string"), mock.AnythingOfType("bool")).Return(true)
	cacheMock.On("Get", mock.AnythingOfType("string")).Return(false, false)

	return cacheMock
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

	t.Run("ValidPHPEntryPoints", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns, NewCacheMock())
		for _, ep := range validEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.True(t, v.IsValid(ep), "Expected entry point '%s' to be valid", ep)
			})
		}
	})

	t.Run("InvalidPHPEntryPoints", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns, NewCacheMock())
		for _, ep := range invalidEntryPoints {
			t.Run(ep, func(t *testing.T) {
				assert.False(t, v.IsValid(ep), "Expected entry point '%s' to be invalid", ep)
			})
		}
	})

	t.Run("EmptyPatterns", func(t *testing.T) {
		t.Parallel()
		v := validator.New([]string{}, NewCacheMock())
		assert.True(t, v.IsValid("any/entry/point"), "Any entry point should be valid when no patterns are defined")
	})

	t.Run("EmptyEntryPoint", func(t *testing.T) {
		t.Parallel()
		v := validator.New(patterns, NewCacheMock())
		assert.False(t, v.IsValid(""), "Empty entry point should be invalid if patterns are defined")
	})

	t.Run("CacheUsage", func(t *testing.T) {
		t.Parallel()

		cacheMock := NewCacheMock()
		cacheMock.On("Add", "index.php", true).Return(true)
		cacheMock.On("Get", "index.php").Return(false, false).Once()

		v := validator.New(patterns, cacheMock)
		entryPoint := "index.php"

		v.IsValid(entryPoint)

		cacheMock.AssertCalled(t, "Add", entryPoint, true)
		cacheMock.AssertCalled(t, "Get", entryPoint)
	})
}
