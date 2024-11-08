package phpspy

import (
	"bufio"
	"context"
	"gospy/internal/types"
	"strings"
	"testing"
)

// TestParser_Parse tests the Parse method of the Parser struct.
func TestParser_Parse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		input                string
		entryPoints          []string
		tagsMapping          map[string]string
		tagEntrypoint        bool
		keepEntrypointName   bool
		expectedFoldedStacks [][2]string
	}{
		{
			name: "Valid trace with meta",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php:30
# server.REQUEST_URI = /index.php

`,
			entryPoints:        []string{"/path/to/fileC.php"},
			tagsMapping:        map[string]string{"server.REQUEST_URI": "uri"},
			tagEntrypoint:      true,
			keepEntrypointName: false,
			expectedFoldedStacks: [][2]string{
				{"functionC;functionB;functionA", "uri=/index.php,entrypoint=/path/to/fileC.php"},
			},
		},
		{
			name: "Trace with invalid entrypoint",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/other.php:30

`,
			entryPoints:          []string{"/path/to/fileC.php"},
			tagsMapping:          map[string]string{},
			tagEntrypoint:        false,
			keepEntrypointName:   false,
			expectedFoldedStacks: [][2]string{},
		},
		{
			name: "Trace with keepEntrypointName",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php:30

`,
			entryPoints:        []string{"/path/to/fileC.php"},
			tagsMapping:        map[string]string{},
			tagEntrypoint:      false,
			keepEntrypointName: true,
			expectedFoldedStacks: [][2]string{
				{"functionC /path/to/fileC.php;functionB;functionA", ""},
			},
		},
		{
			name: "Trace with invalid trace line",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php

`,
			entryPoints:          []string{"/path/to/fileC.php"},
			tagsMapping:          map[string]string{},
			tagEntrypoint:        false,
			keepEntrypointName:   false,
			expectedFoldedStacks: [][2]string{},
		},
		{
			name: "Empty input",
			input: `

`,
			entryPoints:          []string{"/path/to/fileC.php"},
			tagsMapping:          map[string]string{},
			tagEntrypoint:        false,
			keepEntrypointName:   false,
			expectedFoldedStacks: [][2]string{},
		},
		{
			name: "Trace with meta not in tagsMapping",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php:30
# unknown.meta = value

`,
			entryPoints:        []string{"/path/to/fileC.php"},
			tagsMapping:        map[string]string{},
			tagEntrypoint:      true,
			keepEntrypointName: false,
			expectedFoldedStacks: [][2]string{
				{"functionC;functionB;functionA", "entrypoint=/path/to/fileC.php"},
			},
		},
		{
			name: "Trace with multiple meta lines",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php:30
# server.REQUEST_URI = /index.php
# server.REQUEST_METHOD = GET

`,
			entryPoints:        []string{"/path/to/fileC.php"},
			tagsMapping:        map[string]string{"server.REQUEST_URI": "uri", "server.REQUEST_METHOD": "method"},
			tagEntrypoint:      true,
			keepEntrypointName: false,
			expectedFoldedStacks: [][2]string{
				{"functionC;functionB;functionA", "uri=/index.php,method=GET,entrypoint=/path/to/fileC.php"},
			},
		},
		{
			name: "Trace not in entryPoints",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/other_entry.php:30

`,
			entryPoints:          []string{"/path/to/fileC.php"},
			tagsMapping:          map[string]string{},
			tagEntrypoint:        false,
			keepEntrypointName:   false,
			expectedFoldedStacks: [][2]string{},
		},
		{
			name: "Trace with allowed entrypoint and empty meta",
			input: `
2 functionA /path/to/fileA.php:10
1 functionB /path/to/fileB.php:20
0 functionC /path/to/fileC.php:30

`,
			entryPoints:        []string{"/path/to/fileC.php"},
			tagsMapping:        map[string]string{},
			tagEntrypoint:      true,
			keepEntrypointName: false,
			expectedFoldedStacks: [][2]string{
				{"functionC;functionB;functionA", "entrypoint=/path/to/fileC.php"},
			},
		},
		{
			name: "Trace too small",
			input: `
0 main /path/to/index.php:1

`,
			entryPoints:          []string{"/path/to/index.php"},
			tagsMapping:          map[string]string{},
			tagEntrypoint:        false,
			keepEntrypointName:   false,
			expectedFoldedStacks: [][2]string{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			scanner := bufio.NewScanner(strings.NewReader(tc.input))

			foldedStacks := make(chan *types.Sample, 10)

			parser := NewParser(tc.entryPoints, tc.tagsMapping, tc.tagEntrypoint, tc.keepEntrypointName)

			go func() {
				parser.Parse(ctx, scanner, foldedStacks)
				close(foldedStacks)
			}()

			var results []*types.Sample
			for fs := range foldedStacks {
				results = append(results, fs)
			}

			if len(results) != len(tc.expectedFoldedStacks) {
				t.Errorf("Expected %d folded stacks, got %d", len(tc.expectedFoldedStacks), len(results))
			}

			for i, expected := range tc.expectedFoldedStacks {
				if i >= len(results) {
					break
				}
				if results[i].Trace != expected[0] || results[i].Tags != expected[1] {
					t.Errorf("Expected folded stack %v, got %v", expected, results[i])
				}
			}
		})
	}
}

func BenchmarkParseMeta(b *testing.B) {
	lines := []string{
		"# key1 = value1",
		"# key2 = value2",
		"# key3 = value3",
	}
	tagsMapping := map[string]string{
		"key1": "tag1",
		"key2": "tag2",
		"key3": "tag3",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseMeta(lines, tagsMapping)
	}
}
