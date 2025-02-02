package obfuscation_test

import (
	"testing"

	"github.com/hakastein/gospy/internal/obfuscation"
	"github.com/stretchr/testify/require"
)

func TestMaskString(t *testing.T) {
	t.Run("Default parameters", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected string
		}{
			{name: "6 characters", input: "abcdef", expected: "a****f"},
			{name: "5 characters", input: "abcde", expected: "a***e"},
			{name: "3 characters", input: "abc", expected: "a*c"},
			{name: "2 characters", input: "ab", expected: "**"},
			{name: "1 character", input: "a", expected: "*"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.expected, obfuscation.MaskString(tc.input))
			})
		}
	})

	t.Run("Custom parameters", func(t *testing.T) {
		tests := []struct {
			name       string
			input      string
			start, end int
			expected   string
		}{
			{name: "Larger keep values", input: "abcdefghij", start: 3, end: 2, expected: "abc*****ij"},
			{name: "Zero keep values", input: "password", start: 0, end: 0, expected: "********"},
			{name: "Start only", input: "creditcard", start: 4, end: 0, expected: "cred******"},
			{name: "Exact length threshold", input: "abcd", start: 2, end: 2, expected: "****"},
			{name: "Negative keep values", input: "test", start: -1, end: -1, expected: "****"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.expected, obfuscation.MaskString(tc.input, tc.start, tc.end))
			})
		}
	})

	t.Run("Unicode handling", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected string
		}{
			{name: "Cyrillic text", input: "ПриветМир", expected: "П*******р"},
			{name: "Emoji handling", input: "👍🐶🍕🚀", expected: "👍**🚀"},
			{name: "Mixed runes", input: "a⌘cデd", expected: "a***d"},
			{name: "Japanese with latin", input: "日本語test", expected: "日*****t"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.expected, obfuscation.MaskString(tc.input))
			})
		}
	})

	t.Run("Parameter variations", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			params   []int
			expected string
		}{
			{name: "Single parameter", input: "abcdef", params: []int{2}, expected: "ab***f"},
			{name: "Extra parameters ignored", input: "abcdef", params: []int{1, 2, 3}, expected: "a***ef"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.expected, obfuscation.MaskString(tc.input, tc.params...))
			})
		}
	})
}
