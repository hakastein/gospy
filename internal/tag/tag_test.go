package tag

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseInput(t *testing.T) {
	tests := []struct {
		name        string
		input       []string
		wantStatic  string
		wantDynamic map[string][]DynamicTag
		wantErr     bool
	}{
		{
			name: "Only static tags",
			input: []string{
				"env=production",
				"version=1.0.0",
			},
			wantStatic:  "env=production,version=1.0.0",
			wantDynamic: map[string][]DynamicTag{},
			wantErr:     false,
		},
		{
			name: "Only dynamic tag with one part",
			input: []string{
				`request_id={{"id"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"id": {
					{
						TagKey: "request_id",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Dynamic tag with regex and replacement",
			input: []string{
				`user={{"username" "^[a-z]+$" "user_$1"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"username": {
					{
						TagKey:     "user",
						TagRegexp:  regexp.MustCompile("^[a-z]+$"),
						TagReplace: "user_$1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Mixed static and dynamic tags",
			input: []string{
				"env=staging",
				`user={{"username"}}`,
				"version=2.1",
			},
			wantStatic: "env=staging,version=2.1",
			wantDynamic: map[string][]DynamicTag{
				"username": {
					{
						TagKey: "user",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid tag format missing equal sign",
			input: []string{
				"envproduction",
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name: "Invalid tag key with disallowed characters",
			input: []string{
				"env$=production",
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name: "Dynamic tag with invalid number of parameters",
			input: []string{
				`user={{"username" "regex"}}`,
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name: "Dynamic tag with invalid regex",
			input: []string{
				`user={{"username" "[A-Z+" "user_$1"}}`,
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name: "Static tag containing comma",
			input: []string{
				"env=prod,uction",
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name: "Dynamic tag with escaped quotes",
			input: []string{
				`description={{"desc" "He said \"Hello\"" "Greeting: $1"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"desc": {
					{
						TagKey:     "description",
						TagRegexp:  regexp.MustCompile(`He said "Hello"`),
						TagReplace: "Greeting: $1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Dynamic tag with extra parameters",
			input: []string{
				`user={{"username" "regex" "replace" "extra"}}`,
			},
			wantStatic:  "",
			wantDynamic: nil,
			wantErr:     true,
		},
		{
			name:        "Empty input",
			input:       []string{},
			wantStatic:  "",
			wantDynamic: map[string][]DynamicTag{},
			wantErr:     false,
		},
		{
			name: "Dynamic tag with spaces between quotes",
			input: []string{
				`meta={{ "key" "regex" "replace" }}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"key": {
					{
						TagKey:     "meta",
						TagRegexp:  regexp.MustCompile("regex"),
						TagReplace: "replace",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Multiple dynamic tags",
			input: []string{
				`user={{"username"}}`,
				`session={{"session_id" "^[0-9]+$" "sess_$1"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"username": {
					{
						TagKey: "user",
					},
				},
				"session_id": {
					{
						TagKey:     "session",
						TagRegexp:  regexp.MustCompile("^[0-9]+$"),
						TagReplace: "sess_$1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Multiple dynamic tags with same source",
			input: []string{
				`session={{"session_id"}}`,
				`session_id={{"session_id" "^[0-9]+$" "sess_$1"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"session_id": {
					{
						TagKey: "session",
					},
					{
						TagKey:     "session_id",
						TagRegexp:  regexp.MustCompile("^[0-9]+$"),
						TagReplace: "sess_$1",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Static and dynamic tags with similar keys",
			input: []string{
				"env=production",
				`env={{"environment"}}`,
			},
			wantStatic: "env=production",
			wantDynamic: map[string][]DynamicTag{
				"environment": {
					{
						TagKey: "env",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Dynamic tag with empty replacement",
			input: []string{
				`user={{"username" "regex" ""}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"username": {
					{
						TagKey:     "user",
						TagRegexp:  regexp.MustCompile("regex"),
						TagReplace: "",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Multiple dynamic tags with the same key",
			input: []string{
				`user={{"username"}}`,
				`user={{"user_id" "^[0-9]+$" "user_$1"}}`,
			},
			wantStatic: "",
			wantDynamic: map[string][]DynamicTag{
				"username": {
					{
						TagKey: "user",
					},
				},
				"user_id": {
					{
						TagKey:     "user",
						TagRegexp:  regexp.MustCompile("^[0-9]+$"),
						TagReplace: "user_$1",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			gotStatic, gotDynamic, err := ParseInput(tt.input)
			if tt.wantErr {
				assert.Error(t, err, "Expected an error but got none")
				return
			}
			assert.NoError(t, err, "Did not expect an error but got one")

			assert.Equal(t, tt.wantStatic, gotStatic, "Static tags do not match")

			if tt.wantDynamic == nil {
				assert.Nil(t, gotDynamic, "Expected dynamic tags to be nil")
			} else {
				assert.Equal(t, len(tt.wantDynamic), len(gotDynamic), "Number of dynamic keys does not match")

				for key, expectedTags := range tt.wantDynamic {
					actualTags, exists := gotDynamic[key]
					if assert.True(t, exists, "Dynamic tag key %v does not exist", key) {
						assert.Equal(t, len(expectedTags), len(actualTags), "Number of DynamicTags for key %v does not match", key)
						for i, expectedTag := range expectedTags {
							actualTag := actualTags[i]
							assert.Equal(t, expectedTag.TagKey, actualTag.TagKey, "TagKey mismatch for key %v", key)

							if expectedTag.TagRegexp == nil {
								assert.Nil(t, actualTag.TagRegexp, "Expected TagRegexp to be nil for key %v", key)
							} else {
								if assert.NotNil(t, actualTag.TagRegexp, "Expected TagRegexp to be non-nil for key %v", key) {
									assert.Equal(t, expectedTag.TagRegexp.String(), actualTag.TagRegexp.String(), "TagRegexp mismatch for key %v", key)
								}
							}

							assert.Equal(t, expectedTag.TagReplace, actualTag.TagReplace, "TagReplace mismatch for key %v", key)
						}
					}
				}
			}
		})
	}
}
