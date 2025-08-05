package phpspy_test

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/stretchr/testify/require"
)

type parserTestCase struct {
	name               string
	input              []string // each element represents a complete trace block
	entryPoints        []string
	tagsMapping        map[string][]tag.DynamicTag
	tagEntrypoint      bool
	keepEntrypointName bool
	expectedSamples    []collector.Sample // verify exact data, not just count
}

func newScannerFromInput(input []string) *bufio.Scanner {
	inputStr := strings.Join(input, "\n\n") + "\n\n"
	return bufio.NewScanner(strings.NewReader(inputStr))
}

func TestParser_Parse(t *testing.T) {
	testCases := []parserTestCase{
		{
			name: "entrypoint filtering - allows only matching entrypoints",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/allowed.php:1",
				"0 func2 /app/some/helper.php:20\n1 main /app/blocked.php:1",
				"0 func3 /app/some/helper.php:30\n1 main /app/allowed.php:1",
				"0 func4 /app/some/helper.php:40\n1 main /app/blocked.php:1",
			},
			entryPoints: []string{"/app/allowed.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
				{Trace: "main;func3", Tags: ""},
			},
		},
		{
			name: "entrypoint tag addition - adds correct entrypoint to each trace",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/test1.php:1",
				"0 func2 /app/some/helper.php:20\n1 main /app/test2.php:1",
			},
			entryPoints:   []string{"/app/test1.php", "/app/test2.php"},
			tagEntrypoint: true,
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "entrypoint=/app/test1.php"},
				{Trace: "main;func2", Tags: "entrypoint=/app/test2.php"},
			},
		},
		{
			name: "entrypoint tag addition - no entrypoint in tags when disabled",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/test1.php:1",
				"0 func2 /app/some/helper.php:20\n1 main /app/test2.php:1",
			},
			entryPoints:   []string{"/app/test1.php", "/app/test2.php"},
			tagEntrypoint: false,
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
				{Trace: "main;func2", Tags: ""},
			},
		},
		{
			name: "metadata processing - processes metadata lines correctly",
			input: []string{
				"# glopeek test.key = value1\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
				"# glopeek test.key = value2\n0 func2 /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{"glopeek test.key": {{TagKey: "test"}}},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "test=value1"},
				{Trace: "main;func2", Tags: "test=value2"},
			},
		},
		{
			name: "metadata with entrypoint tag - combines metadata and entrypoint tags correctly",
			input: []string{
				"# glopeek test.key = value1\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
				"# glopeek test.key = value2\n0 func2 /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints:   []string{"/app/test.php"},
			tagsMapping:   map[string][]tag.DynamicTag{"glopeek test.key": {{TagKey: "test"}}},
			tagEntrypoint: true,
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "test=value1,entrypoint=/app/test.php"},
				{Trace: "main;func2", Tags: "test=value2,entrypoint=/app/test.php"},
			},
		},
		{
			name: "scanner line processing - handles empty lines and whitespace",
			input: []string{
				"   \n\n  \n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1\n   \n",
				"0 func2 /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
				{Trace: "main;func2", Tags: ""},
			},
		},
		{
			name: "scanner line processing - processes metadata lines starting with #",
			input: []string{
				"#metadata line\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
				"# another metadata\n0 func2 /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
				{Trace: "main;func2", Tags: ""},
			},
		},
		{
			name: "empty entrypoints - allows all traces when entryPoints is empty",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/any1.php:1",
				"0 func2 /app/some/helper.php:20\n1 main /app/any2.php:1",
			},
			entryPoints: []string{}, // empty should allow all
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
				{Trace: "main;func2", Tags: ""},
			},
		},
		{
			name: "invalid trace handling - skips invalid traces but continues processing",
			input: []string{
				"#metatdata line\n/app/index.php",
				"0 valid_func /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main;valid_func", Tags: ""},
			}, // should skip invalid trace but process valid one
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parser := phpspy.NewParser(tc.entryPoints, tc.tagsMapping, tc.tagEntrypoint, tc.keepEntrypointName)

			scanner := newScannerFromInput(tc.input)
			samplesChannel := make(chan *collector.Sample, 100)

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			go func() {
				parser.Parse(ctx, scanner, samplesChannel)
				close(samplesChannel)
			}()

			var samples []*collector.Sample
			for sample := range samplesChannel {
				samples = append(samples, sample)
			}

			// Verify exact number of samples
			require.Len(t, samples, len(tc.expectedSamples))

			// Verify exact data for each sample
			for i, expected := range tc.expectedSamples {
				require.Equal(t, expected.Trace, samples[i].Trace, "Sample %d trace mismatch", i)
				require.Equal(t, expected.Tags, samples[i].Tags, "Sample %d tags mismatch", i)
				require.NotZero(t, samples[i].Time) // parser should set time
			}
		})
	}
}

// TestParser_ParseWithContextCancellation tests that parser stops processing when context is cancelled
func TestParser_ParseWithContextCancellation(t *testing.T) {
	tc := parserTestCase{
		input:       []string{"0 func1 /app/some/helper.php:10\n1 main /app/test.php:1"},
		entryPoints: []string{"/app/test.php"},
	}
	parser := phpspy.NewParser(tc.entryPoints, tc.tagsMapping, tc.tagEntrypoint, tc.keepEntrypointName)

	scanner := newScannerFromInput(tc.input)
	samplesChannel := make(chan *collector.Sample, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Cancel immediately to test cancellation handling
	cancel()

	go func() {
		parser.Parse(ctx, scanner, samplesChannel)
		close(samplesChannel)
	}()

	var samples []*collector.Sample
	for sample := range samplesChannel {
		samples = append(samples, sample)
	}

	// Should have no samples due to immediate cancellation
	require.Len(t, samples, 0)
}

// TestParser_ParseWithScannerError tests scanner error handling
func TestParser_ParseWithScannerError(t *testing.T) {
	parser := phpspy.NewParser([]string{"/app/test.php"}, nil, false, false)

	// Create a reader that will cause scanner error
	reader := &errorReader{}
	scanner := bufio.NewScanner(reader)
	samplesChannel := make(chan *collector.Sample, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		parser.Parse(ctx, scanner, samplesChannel)
		close(samplesChannel)
	}()

	// Should handle error gracefully and not crash
	var samples []*collector.Sample
	for sample := range samplesChannel {
		samples = append(samples, sample)
	}

	// Should have no samples due to scanner error
	require.Len(t, samples, 0)
}

// errorReader simulates a reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated read error")
}
