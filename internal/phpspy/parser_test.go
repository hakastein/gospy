package phpspy_test

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
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
			name: "invalid trace handling - skips malformed traces but continues processing",
			input: []string{
				"#metatdata line\n/app/index.php",
				"0 InvalidTrace /app/src/Module.php\n1 MissingFields",
				"0 StartFunction <internal>:-1\n1 ServiceModule::Handle /app/src/ServiceModule.php",
				"0 <internal>:-1\n1 /app/src/ServiceModule.php:45",
				"0 valid_func /app/some/helper.php:20\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main;valid_func", Tags: ""},
			},
		},
		{
			name: "stack folding - a single frame folds to a one-node stack",
			input: []string{
				"0 main /app/index.php:12",
			},
			entryPoints:   []string{"/app/index.php"},
			tagEntrypoint: true,
			expectedSamples: []collector.Sample{
				{Trace: "main", Tags: "entrypoint=/app/index.php"},
			},
		},
		{
			name: "stack folding - a single frame keeps the entrypoint script when enabled",
			input: []string{
				"0 main /app/index.php:12",
			},
			entryPoints:        []string{"/app/index.php"},
			keepEntrypointName: true,
			expectedSamples: []collector.Sample{
				{Trace: "main /app/index.php", Tags: ""},
			},
		},
		{
			name: "stack folding - a path containing spaces keeps its full location",
			input: []string{
				"0 helper /app/my libs/helper.php:10\n1 main /app/my app/index.php:1",
			},
			entryPoints:        []string{"/app/my app/index.php"},
			keepEntrypointName: true,
			tagEntrypoint:      true,
			expectedSamples: []collector.Sample{
				{Trace: "main /app/my app/index.php;helper", Tags: "entrypoint=/app/my_app/index.php"},
			},
		},
		{
			name: "stack folding - a frame without a location is rejected",
			input: []string{
				"0 main /app/index.php:1",
				"0 main",
				"0 main /app/index.php",
			},
			entryPoints: []string{"/app/index.php"},
			expectedSamples: []collector.Sample{
				{Trace: "main", Tags: ""},
			},
		},
		{
			name: "entrypoint tag - reserved characters in the path are sanitized",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/we=ird}.php:1",
			},
			tagEntrypoint: true,
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "entrypoint=/app/we_ird_.php"},
			},
		},
		{
			name: "stack folding - reverses frames and keeps the entrypoint script when enabled",
			input: []string{
				"0 InitFunction <internal>:-1\n1 ServiceModule::HandleRequest /app/src/ServiceModule.php:45\n2 ServiceModule::Process /app/src/ServiceModule.php:30\n3 Utils::Helper /app/src/Utils.php:15",
			},
			entryPoints:        []string{"/app/src/Utils.php"},
			keepEntrypointName: true,
			expectedSamples: []collector.Sample{
				{Trace: "Utils::Helper /app/src/Utils.php;ServiceModule::Process;ServiceModule::HandleRequest;InitFunction", Tags: ""},
			},
		},
		{
			name: "stack folding - drops the entrypoint script when disabled",
			input: []string{
				"0 InitFunction <internal>:-1\n1 ServiceModule::HandleRequest /app/src/ServiceModule.php:45\n2 ServiceModule::Process /app/src/ServiceModule.php:30\n3 Utils::Helper /app/src/Utils.php:15",
			},
			entryPoints: []string{"/app/src/Utils.php"},
			expectedSamples: []collector.Sample{
				{Trace: "Utils::Helper;ServiceModule::Process;ServiceModule::HandleRequest;InitFunction", Tags: ""},
			},
		},
		{
			name: "dynamic tags - sorted by tag key",
			input: []string{
				"# version = 1.0\n# license = MIT\n# author = John Doe\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"version": {{TagKey: "v"}},
				"author":  {{TagKey: "creator"}},
				"license": {{TagKey: "lic"}},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "creator=John_Doe,lic=MIT,v=1.0"},
			},
		},
		{
			name: "dynamic tags - value is trimmed and inner reserved characters are sanitized",
			input: []string{
				"# description =              Version 1.0 = Initial Release         \n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"description": {{TagKey: "description"}},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "description=Version_1.0___Initial_Release"},
			},
		},
		{
			name: "dynamic tags - one meta key feeds several tags with regexp rewrite",
			input: []string{
				"# greetings = Hello World\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"greetings": {
					{TagKey: "hi", TagRegexp: regexp.MustCompile("World"), TagReplace: "Sekai"},
					{TagKey: "hello"},
				},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "hello=Hello_World,hi=Hello_Sekai"},
			},
		},
		{
			name: "dynamic tags - last occurrence of a tag key wins",
			input: []string{
				"# author = Alice\n# writer = Bob\n# author = Charlie\n# writer = Dave\n# version = 3.1\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"author":  {{TagKey: "creator"}},
				"writer":  {{TagKey: "creator"}},
				"version": {{TagKey: "v"}},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "creator=Dave,v=3.1"},
			},
		},
		{
			name: "dynamic tags - unmapped and malformed meta lines are ignored",
			input: []string{
				"# keywithoutmapping = value\n# validKey = validValue\n# badformat\n#validKey=ignored\n# anotherBad = format1 = format2\n# author = \n# validKey = newValue\n0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"validKey": {{TagKey: "mappedValid"}},
				"author":   {{TagKey: "creator"}},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: "creator=,mappedValid=newValue"},
			},
		},
		{
			name: "dynamic tags - a trace block without meta lines carries no tags",
			input: []string{
				"0 func1 /app/some/helper.php:10\n1 main /app/test.php:1",
			},
			entryPoints: []string{"/app/test.php"},
			tagsMapping: map[string][]tag.DynamicTag{
				"author": {{TagKey: "creator"}},
			},
			expectedSamples: []collector.Sample{
				{Trace: "main;func1", Tags: ""},
			},
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

func TestParserParseReturnsOnCancellationWhileChannelIsBlocked(t *testing.T) {
	parser := phpspy.NewParser([]string{"/app/test.php"}, nil, false, false)
	scanner := bufio.NewScanner(strings.NewReader("0 func1 /app/helper.php:10\n1 main /app/test.php:1\n\n"))
	samplesChannel := make(chan *collector.Sample)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		parser.Parse(ctx, scanner, samplesChannel)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Parse did not return after context cancellation")
	}
}

func TestParserParseFlushesFinalTraceOnEOF(t *testing.T) {
	parser := phpspy.NewParser([]string{"/app/test.php"}, nil, false, false)
	scanner := bufio.NewScanner(strings.NewReader("0 func1 /app/helper.php:10\n1 main /app/test.php:1"))
	samplesChannel := make(chan *collector.Sample, 1)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parser.Parse(ctx, scanner, samplesChannel)
	close(samplesChannel)

	var samples []*collector.Sample
	for sample := range samplesChannel {
		samples = append(samples, sample)
	}

	require.Len(t, samples, 1)
	require.Equal(t, "main;func1", samples[0].Trace)
}

// errorReader simulates a reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated read error")
}
