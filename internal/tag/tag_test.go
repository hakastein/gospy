package tag_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/tag"
)

func TestParseInput(t *testing.T) {
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
				name:        "Static Tag with Surrounding Whitespace",
				input:       []string{"  env = production  "},
				wantStatic:  "env=production",
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
				name:  "Dynamic Tag with Trailing Whitespace",
				input: []string{`uri={{ "glopeek server.REQUEST_URI" }} `},
				wantDynamic: map[string][]tag.DynamicTag{
					"glopeek server.REQUEST_URI": {{
						TagKey: "uri",
					}},
				},
			},
			{
				name:  "Dynamic Tag with Leading Whitespace",
				input: []string{` uri={{"glopeek server.REQUEST_URI"}}`},
				wantDynamic: map[string][]tag.DynamicTag{
					"glopeek server.REQUEST_URI": {{
						TagKey: "uri",
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
				require.NoError(t, err)
				require.Equal(t, tt.wantStatic, static)
				requireSameDynamic(t, tt.wantDynamic, dynamic)
			})
		}
	})

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
				name:  "Empty Tag Key",
				input: []string{"=production"},
			},
			{
				name:  "Blank Tag Key",
				input: []string{"   =production"},
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
				name:  "Dynamic Tag without Closing Braces",
				input: []string{`user={{"username"`},
			},
			{
				name:  "Dynamic Tag with Trailing Space after Closing Braces and Missing Quote",
				input: []string{`user={{"username }} `},
			},
			{
				name:  "Dynamic Tag Opened but Empty",
				input: []string{`user={{}}`},
			},
			{
				name:  "Static Tag with Comma",
				input: []string{"env=prod,uction"},
			},
			{
				name:  "Static Tag with Opening Brace",
				input: []string{"env=prod{uction"},
			},
			{
				name:  "Static Tag with Closing Brace",
				input: []string{"env=prod}uction"},
			},
			{
				name:  "Static Tag with Equals Sign",
				input: []string{"env=prod=uction"},
			},
			{
				name:  "Static Tag with Inner Whitespace",
				input: []string{"env=prod uction"},
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
				require.Error(t, err)
			})
		}
	})

	t.Run("ErrorNamesTheTag", func(t *testing.T) {
		_, _, err := tag.ParseInput([]string{`uri={{"glopeek server.REQUEST_URI"`})
		require.Error(t, err)
		require.Contains(t, err.Error(), "uri")
	})
}

func requireSameDynamic(t *testing.T, want, got map[string][]tag.DynamicTag) {
	t.Helper()

	require.Len(t, got, len(want))
	for key, wantTags := range want {
		gotTags, ok := got[key]
		require.True(t, ok, "key %q not found", key)
		require.Len(t, gotTags, len(wantTags))
		for i := range wantTags {
			require.Equal(t, wantTags[i].TagKey, gotTags[i].TagKey)
			if wantTags[i].TagRegexp == nil {
				require.Nil(t, gotTags[i].TagRegexp)
			} else {
				require.NotNil(t, gotTags[i].TagRegexp)
				require.Equal(t, wantTags[i].TagRegexp.String(), gotTags[i].TagRegexp.String())
			}
			require.Equal(t, wantTags[i].TagReplace, gotTags[i].TagReplace)
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
			name:     "Empty Replacement Strips the Match",
			tag:      tag.DynamicTag{TagKey: "uri", TagRegexp: regexp.MustCompile(`\?.*$`)},
			input:    "/orders?id=7",
			expected: "/orders",
		},
		{
			name:     "Regex and Comma Replacement",
			tag:      tag.DynamicTag{TagKey: "test", TagRegexp: regexp.MustCompile("a"), TagReplace: "b"},
			input:    "a,a",
			expected: "b͵b",
		},
		{
			name:     "Hostile Value Is Sanitized",
			tag:      tag.DynamicTag{TagKey: "uri"},
			input:    `/x}}, evil=1 {`,
			expected: "/x__͵_evil_1__",
		},
		{
			name:     "Whitespace and Quotes Are Sanitized",
			tag:      tag.DynamicTag{TagKey: "uri"},
			input:    "a\tb\nc\"d",
			expected: "a_b_c_d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tag.GetValue(tt.input)
			require.Equal(t, tt.expected, got)
			require.False(t, strings.ContainsAny(got, `{}=,"`), "sanitized value still carries reserved characters")
		})
	}
}

func TestSanitizeValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain Value Is Untouched",
			input:    "/app/index.php",
			expected: "/app/index.php",
		},
		{
			name:     "Empty Value",
			input:    "",
			expected: "",
		},
		{
			name:     "Comma Becomes the Greek Lower Numeral Sign",
			input:    "a,b",
			expected: "a͵b",
		},
		{
			name:     "Reserved Characters Become Underscores",
			input:    `{a}=b "c" d`,
			expected: "_a__b__c__d",
		},
		{
			name:     "Control Characters Become Underscores",
			input:    "a\x00b",
			expected: "a_b",
		},
		{
			name:     "Non-ASCII Characters Survive",
			input:    "путь/файл.php",
			expected: "путь/файл.php",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tag.SanitizeValue(tt.input))
		})
	}
}
