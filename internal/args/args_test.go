package args_test

import (
	"testing"

	"github.com/hakastein/gospy/internal/args" // replace with actual module path
	"github.com/stretchr/testify/require"
)

func TestExtractFlagValue(t *testing.T) {
	t.Run("string flags", func(t *testing.T) {
		testCases := []struct {
			name     string
			flags    []string
			expected string
		}{
			{
				name:     "long key with value",
				flags:    []string{"--name=Alice"},
				expected: "Alice",
			},
			{
				name:     "short key with separate value",
				flags:    []string{"-n", "Bob"},
				expected: "Bob",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := args.ExtractFlagValue[string](
					tc.flags,
					"name",
					"n",
					"default",
				)
				require.Equal(t, tc.expected, result)
			})
		}
	})

	t.Run("integer flags", func(t *testing.T) {
		testCases := []struct {
			name     string
			flags    []string
			expected int
		}{
			{
				name:     "valid integer value",
				flags:    []string{"--count=42"},
				expected: 42,
			},
			{
				name:     "invalid integer returns default",
				flags:    []string{"--count=invalid"},
				expected: 0,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := args.ExtractFlagValue[int](
					tc.flags,
					"count",
					"c",
					0,
				)
				require.Equal(t, tc.expected, result)
			})
		}
	})

	t.Run("boolean flags", func(t *testing.T) {
		testCases := []struct {
			name     string
			flags    []string
			expected bool
		}{
			{
				name:     "standalone flag returns true",
				flags:    []string{"--verbose"},
				expected: true,
			},
			{
				name:     "explicit false value",
				flags:    []string{"--verbose=false"},
				expected: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := args.ExtractFlagValue[bool](
					tc.flags,
					"verbose",
					"v",
					false,
				)
				require.Equal(t, tc.expected, result)
			})
		}
	})

	t.Run("edge cases", func(t *testing.T) {
		t.Run("empty flags return default", func(t *testing.T) {
			result := args.ExtractFlagValue[int](nil, "count", "c", 42)
			require.Equal(t, 42, result)
		})

		t.Run("capture subsequent flag as value", func(t *testing.T) {
			result := args.ExtractFlagValue[string](
				[]string{"-n", "--other"},
				"name",
				"n",
				"default",
			)
			require.Equal(t, "--other", result)
		})
	})
}
