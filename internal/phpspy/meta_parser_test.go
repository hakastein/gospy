package phpspy

import (
	"gospy/internal/tag"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// parseMetaTest defines the structure for each test case.
type parseMetaTest struct {
	name        string
	lines       []string
	tagsMapping map[string][]tag.DynamicTag
	want        string
}

// runParseMetaTests executes a slice of parseMetaTest cases.
func runParseMetaTests(t *testing.T, tests []parseMetaTest, assertMessage string) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMeta(tt.lines, tt.tagsMapping)
			assert.Equal(t, tt.want, got, assertMessage)
		})
	}
}

// TestParseMeta tests the parseMeta function across various scenarios.
func TestParseMeta(t *testing.T) {
	validInputs := []parseMetaTest{
		{
			name:  "Single valid line",
			lines: []string{"# author = John Doe"},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {
					{TagKey: "creator"},
				},
			},
			want: "creator=John Doe",
		},
		{
			name: "Mapped keys in alphabetical order",
			lines: []string{
				"# version = 1.0",
				"# license = MIT",
				"# author = John Doe",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"version": {
					{TagKey: "v"},
				},
				"author": {
					{TagKey: "creator"},
				},
				"license": {
					{TagKey: "lic"},
				},
			},
			want: "creator=John Doe,lic=MIT,v=1.0",
		},
		{
			name: "Equal sign in value",
			lines: []string{
				"# author = Jane = Doe",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"description": {
					{TagKey: "desc"},
				},
				"author": {
					{TagKey: "creator"},
				},
			},
			want: "creator=Jane = Doe",
		},
		{
			name: "Leading spaces in value",
			lines: []string{
				"# description =              Version 1.0 = Initial Release         ",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"description": {
					{TagKey: "description"},
				},
			},
			want: "description=Version 1.0 = Initial Release",
		},
		{
			name: "Spaces in key",
			lines: []string{
				"# description of process = description value",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"description of process": {
					{TagKey: "description"},
				},
			},
			want: "description=description value",
		},
	}

	invalidFormats := []parseMetaTest{
		{
			name:        "Empty lines",
			lines:       []string{},
			tagsMapping: map[string][]tag.DynamicTag{"author": {{TagKey: "creator"}}},
			want:        "",
		},
		{
			name:        "No '# ' prefix",
			lines:       []string{"invalid line", "another invalid line"},
			tagsMapping: map[string][]tag.DynamicTag{"author": {{TagKey: "creator"}}},
			want:        "",
		},
		{
			name:  "Invalid '# ' format",
			lines: []string{"#invalid=123", "#anotherinvalid"},
			tagsMapping: map[string][]tag.DynamicTag{
				"invalid":        {{TagKey: "invalid"}},
				"anotherinvalid": {{TagKey: "anotherinvalid"}},
			},
			want: "",
		},
		{
			name:  "Lines with '# ' but bad format",
			lines: []string{"# keyonly", "# keymultiple=val1=val2"},
			tagsMapping: map[string][]tag.DynamicTag{
				"keyonly":     {{TagKey: "mappedKey"}},
				"keymultiple": {{TagKey: "val1"}},
			},
			want: "",
		},
		{
			name:        "Keys not in mapping",
			lines:       []string{"# unknown = value", "# anotherUnknown = value2"},
			tagsMapping: map[string][]tag.DynamicTag{"author": {{TagKey: "creator"}}},
			want:        "",
		},
	}

	duplicateKeys := []parseMetaTest{
		{
			name: "Duplicate keys - last occurrence retained",
			lines: []string{
				"# author = Bob",
				"# author = Charlie",
				"# version = 1.2",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {
					{TagKey: "creator"},
				},
				"version": {
					{TagKey: "v"},
				},
			},
			want: "creator=Charlie,v=1.2",
		},
		{
			name: "Same source but different values",
			lines: []string{
				"# greetings = Hello World",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"greetings": {
					{
						TagKey:     "hi",
						TagRegexp:  regexp.MustCompile("World"),
						TagReplace: "Sekai",
					},
					{
						TagKey: "hello",
					},
				},
			},
			want: "hello=Hello World,hi=Hello Sekai",
		},
		{
			name: "Mapped keys to the same target key - last occurrence retained",
			lines: []string{
				"# author = Alice",
				"# writer = Bob",
				"# version = 2.0",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {
					{TagKey: "creator"},
				},
				"writer": {
					{TagKey: "creator"},
				},
				"version": {
					{TagKey: "v"},
				},
			},
			want: "creator=Bob,v=2.0",
		},
		{
			name: "Combined duplicate and mapped keys",
			lines: []string{
				"# author = Alice",
				"# writer = Bob",
				"# author = Charlie",
				"# writer = Dave",
				"# version = 3.1",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {
					{TagKey: "creator"},
				},
				"writer": {
					{TagKey: "creator"},
				},
				"version": {
					{TagKey: "v"},
				},
			},
			want: "creator=Dave,v=3.1",
		},
	}

	edgeCases := []parseMetaTest{
		{
			name: "Mixed valid and invalid lines",
			lines: []string{
				"invalid line",
				"# keywithoutmapping = value",
				"# validKey = validValue",
				"# badformat",
				"# anotherBad = format1 = format2",
				"# validKey = newValue",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"validKey": {
					{TagKey: "mappedValid"},
				},
			},
			want: "mappedValid=newValue",
		},
		{
			name: "Empty key and value",
			lines: []string{
				"#  = ",
				"# author = ",
				"# = value",
				"# key = value",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {
					{TagKey: "creator"},
				},
				"key": {
					{TagKey: "k"},
				},
			},
			want: "creator=,k=value",
		},
		{
			name: "Special characters in keys and values",
			lines: []string{
				"# auth@or = Jo!hn_Doe",
				"# vers#on = 1.0$",
				"# lic#ence = M!T",
			},
			tagsMapping: map[string][]tag.DynamicTag{
				"auth@or": {
					{TagKey: "creator"},
				},
				"vers#on": {
					{TagKey: "v"},
				},
				"lic#ence": {
					{TagKey: "lic"},
				},
			},
			want: "creator=Jo!hn_Doe,lic=M!T,v=1.0$",
		},
	}

	t.Run("Valid Inputs", func(t *testing.T) {
		runParseMetaTests(t, validInputs, "parseMeta() should return the expected result")
	})

	t.Run("Invalid Formats", func(t *testing.T) {
		runParseMetaTests(t, invalidFormats, "parseMeta() should return empty string for invalid inputs")
	})

	t.Run("Duplicate Keys", func(t *testing.T) {
		runParseMetaTests(t, duplicateKeys, "parseMeta() should retain the last occurrence of duplicate keys")
	})

	t.Run("Edge Cases", func(t *testing.T) {
		runParseMetaTests(t, edgeCases, "parseMeta() should handle edge cases correctly")
	})
}
