package phpspy_test

import (
	"bufio"
	"context"
	"github.com/hakastein/gospy/internal/phpspy"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/types"
	"os"
	"testing"
)

func TestParser_Parse(t *testing.T) {
	traces, terr := os.Open("testdata/traces.txt")
	if terr != nil {
		t.Fatalf("Failed to open test traces: %v", terr)
	}
	defer traces.Close()

	stacks, serr := os.Open("testdata/stacks.txt")
	if serr != nil {
		t.Fatalf("Failed to open test stacks: %v", serr)
	}
	defer stacks.Close()

	tracesScanner := bufio.NewScanner(traces)
	samplesChannel := make(chan *types.Sample, 100)

	parser := phpspy.NewParser(
		[]string{"server.php"},
		map[string][]tag.DynamicTag{
			"glopeek server.REQUEST_URI": {
				{TagKey: "uri"},
			},
		},
		false,
		true,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		parser.Parse(ctx, tracesScanner, samplesChannel)
		close(samplesChannel)
	}()

	var samples []string
	for sample := range samplesChannel {
		samples = append(samples, sample.Tags+" "+sample.Trace)
	}

	var expectedStacks []string
	stacksScanner := bufio.NewScanner(stacks)
	for stacksScanner.Scan() {
		expectedStacks = append(expectedStacks, stacksScanner.Text())
	}

	if len(samples) != len(expectedStacks) {
		t.Fatalf("Number of samples does not match number of expected stacks: got %d, want %d", len(samples), len(expectedStacks))
	}

	for i, sample := range samples {
		if sample != expectedStacks[i] {
			t.Errorf("Mismatch in sample %d: got %q, want %q", i, sample, expectedStacks[i])
		}
	}
}
