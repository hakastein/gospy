package tag_test

import (
	"regexp"
	"testing"

	"github.com/hakastein/gospy/internal/tag"
	"github.com/stretchr/testify/assert"
)

func TestParseInput(t *testing.T) {
	// Happy path тесты: корректные входные данные.
	t.Run("HappyPath", func(t *testing.T) {
		tests := []struct {
			name        string
			input       []string
			wantStatic  string
			wantDynamic map[string][]tag.DynamicTag
		}{
			{
				name:        "Static Tags Only",
				input:       []string{"env=production", "version=1.0.0"},
				wantStatic:  "env=production,version=1.0.0",
				wantDynamic: map[string][]tag.DynamicTag{},
			},
			{
				name:  "Dynamic Tag with Single Parameter",
				input: []string{`request_id={{"id"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"id": {{
						TagKey: "request_id",
					}},
				},
			},
			{
				name:  "Dynamic Tag with Regex and Replacement",
				input: []string{`user={{"username" "^[a-z]+$" "user_$1"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"username": {{
						TagKey:     "user",
						TagRegexp:  regexp.MustCompile("^[a-z]+$"),
						TagReplace: "user_$1",
					}},
				},
			},
			{
				name:       "Mixed Static and Dynamic Tags",
				input:      []string{"env=staging", `user={{"username"}}`, "version=2.1"},
				wantStatic: "env=staging,version=2.1",
				wantDynamic: map[string][]tag.DynamicTag{
					"username": {{
						TagKey: "user",
					}},
				},
			},
			{
				name:  "Dynamic Tag with Escaped Quotes",
				input: []string{`description={{"desc" "He said \"Hello\"" "Greeting: $1"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"desc": {{
						TagKey:     "description",
						TagRegexp:  regexp.MustCompile(`He said "Hello"`),
						TagReplace: "Greeting: $1",
					}},
				},
			},
			{
				name:        "Empty Input",
				input:       []string{},
				wantStatic:  "",
				wantDynamic: map[string][]tag.DynamicTag{},
			},
			{
				name:  "Dynamic Tag with Spaces Between Quotes",
				input: []string{`meta={{ "key"   "regex"   "replace"   }}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"key": {{
						TagKey:     "meta",
						TagRegexp:  regexp.MustCompile("regex"),
						TagReplace: "replace",
					}},
				},
			},
			{
				name:  "Multiple Dynamic Tags",
				input: []string{`user={{"username"}}`, `session={{"session_id" "^[0-9]+$" "sess_$1"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"username": {{
						TagKey: "user",
					}},
					"session_id": {{
						TagKey:     "session",
						TagRegexp:  regexp.MustCompile("^[0-9]+$"),
						TagReplace: "sess_$1",
					}},
				},
			},
			{
				name:  "Multiple Dynamic Tags with Same Source Key",
				input: []string{`session={{"session_id"}}`, `session_id={{"session_id" "^[0-9]+$" "sess_$1"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"session_id": {{
						TagKey: "session",
					}, {
						TagKey:     "session_id",
						TagRegexp:  regexp.MustCompile("^[0-9]+$"),
						TagReplace: "sess_$1",
					}},
				},
			},
			{
				name:  "Dynamic Tag with Empty Replacement",
				input: []string{`user={{"username" "regex" ""}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"username": {{
						TagKey:     "user",
						TagRegexp:  regexp.MustCompile("regex"),
						TagReplace: "",
					}},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				static, dynamic, err := tag.ParseInput(tt.input)
				assert.NoError(t, err)
				assert.Equal(t, tt.wantStatic, static)
				compareDynamic(t, tt.wantDynamic, dynamic)
			})
		}
	})

	// Error кейсы: некорректные входные данные.
	t.Run("ErrorCases", func(t *testing.T) {
		tests := []struct {
			name  string
			input []string
		}{
			{
				name:  "Missing Equal Sign",
				input: []string{"envproduction"},
			},
			{
				name:  "Invalid Tag Key Characters",
				input: []string{"env$=production"},
			},
			{
				name:  "Dynamic Tag with Invalid Parameter Count",
				input: []string{`user={{"username" "regex"}}`},
			},
			{
				name:  "Dynamic Tag with Invalid Regex",
				input: []string{`user={{"username" "[A-Z+" "user_$1"}}`},
			},
			{
				name:  "Static Tag with Comma",
				input: []string{"env=prod,uction"},
			},
			{
				name:  "Dynamic Tag with unexpected character (unquoted content)",
				input: []string{`user={{a}}`},
			},
			{
				name:  "Dynamic Tag with unterminated quote",
				input: []string{`user={{"username}}`},
			},
			{
				name:  "Dynamic Tag with unexpected space (backslash outside quotes)",
				input: []string{`user={{\ "regex" "replace"}}`},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, _, err := tag.ParseInput(tt.input)
				assert.Error(t, err)
			})
		}
	})
}

func compareDynamic(t *testing.T, want, got map[string][]tag.DynamicTag) {
	assert.Equal(t, len(want), len(got))
	for key, wTags := range want {
		gTags, ok := got[key]
		assert.True(t, ok, "key %q not found", key)
		assert.Equal(t, len(wTags), len(gTags))
		for i := range wTags {
			assert.Equal(t, wTags[i].TagKey, gTags[i].TagKey)
			if wTags[i].TagRegexp == nil {
				assert.Nil(t, gTags[i].TagRegexp)
			} else {
				assert.NotNil(t, gTags[i].TagRegexp)
				assert.Equal(t, wTags[i].TagRegexp.String(), gTags[i].TagRegexp.String())
			}
			assert.Equal(t, wTags[i].TagReplace, gTags[i].TagReplace)
		}
	}
}

func TestDynamicTag_GetValue(t *testing.T) {
	tests := []struct {
		name     string
		tag      tag.DynamicTag
		input    string
		expected string
	}{
		{
			name:     "No Regex, Comma Replacement",
			tag:      tag.DynamicTag{TagKey: "sample"},
			input:    "abc,def",
			expected: "abc͵def",
		},
		{
			name:     "Regex Replacement",
			tag:      tag.DynamicTag{TagKey: "user", TagRegexp: regexp.MustCompile("foo"), TagReplace: "bar"},
			input:    "foofoo",
			expected: "barbar",
		},
		{
			name:     "Only Comma Replacement",
			tag:      tag.DynamicTag{TagKey: "desc"},
			input:    "hello,world",
			expected: "hello͵world",
		},
		{
			name:     "Regex and Comma Replacement",
			tag:      tag.DynamicTag{TagKey: "test", TagRegexp: regexp.MustCompile("a"), TagReplace: "b"},
			input:    "a,a",
			expected: "b͵b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tt.tag.GetValue(tt.input)
			assert.Equal(t, tt.expected, res)
		})
	}
}
