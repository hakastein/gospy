package phpspy

import (
	"bufio"
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/validator"
)

const (
	traceCapacity = 100
	metaCapacity  = 16
)

// A tag mapping that collides does so on every sample.
const duplicateKeyLogPeriod = time.Minute

// ParserConfig describes how one phpspy stream turns into samples: which entry points are
// kept, the static tags every sample carries, how meta lines map to dynamic tags, and the
// rate every sample is stamped with.
type ParserConfig struct {
	Entrypoints        []string
	StaticTags         string
	DynamicTags        map[string][]tag.DynamicTag
	TagEntrypoint      bool
	KeepEntrypointName bool
	SampleRate         int
}

type BlockOutcome uint8

const (
	Sampled BlockOutcome = iota
	// Partial blocks followed a diagnostic that cut the stack short and were dropped.
	Partial
	// Filtered blocks had an entry point the target does not keep.
	Filtered
	Malformed
)

type Observer interface {
	// Diagnostic is a line phpspy printed about itself rather than about a trace.
	Diagnostic(line string)
	// Block comes before the sample is sent: a stalled collector must not look like a silent phpspy.
	Block(outcome BlockOutcome)
}

type ignoreEverything struct{}

func (ignoreEverything) Diagnostic(string)  {}
func (ignoreEverything) Block(BlockOutcome) {}

type Parser struct {
	observer           Observer
	staticTags         string
	tagsMapping        map[string][]tag.DynamicTag
	tagEntrypoint      bool
	keepEntrypointName bool
	sampleRate         int
	currentTrace       []string
	currentMeta        []string
	partial            bool
	epValidator        *validator.EntryPointValidator
	duplicateKeys      zerolog.Sampler
}

// NewParser expects stdout and stderr as one stream: in -p mode phpspy prints a trace's
// diagnostics before its block, and only that order ties them together.
func NewParser(cfg ParserConfig, observer Observer) *Parser {
	if observer == nil {
		observer = ignoreEverything{}
	}

	return &Parser{
		observer:           observer,
		staticTags:         cfg.StaticTags,
		tagsMapping:        cfg.DynamicTags,
		tagEntrypoint:      cfg.TagEntrypoint,
		keepEntrypointName: cfg.KeepEntrypointName,
		sampleRate:         cfg.SampleRate,
		currentTrace:       make([]string, 0, traceCapacity),
		currentMeta:        make([]string, 0, metaCapacity),
		epValidator:        validator.New(cfg.Entrypoints),
		duplicateKeys:      &zerolog.BurstSampler{Burst: 1, Period: duplicateKeyLogPeriod},
	}
}

// Parse reads trace blocks until the scanner ends and returns the scanner's read error, if any;
// a clean EOF and a cancelled context both return nil. It does not close samples; the caller
// owns closing it.
func (parser *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	samples chan<- *collector.Sample,
) error {
	for {
		if ctx.Err() != nil {
			log.Debug().Msg("parser stopped due to context cancellation")
			return nil
		}

		if !scanner.Scan() {
			return parser.flush(ctx, samples, scanner.Err())
		}

		if err := parser.consumeLine(ctx, samples, scanner.Text()); err != nil {
			return nil
		}
	}
}

func (parser *Parser) consumeLine(
	ctx context.Context,
	samples chan<- *collector.Sample,
	line string,
) error {
	log.Trace().Str("line", line).Msg("read profiler output line")

	switch {
	case strings.TrimSpace(line) == "":
		return parser.processTrace(ctx, samples)
	case strings.HasPrefix(line, "#"):
		if len(parser.tagsMapping) > 0 {
			parser.currentMeta = append(parser.currentMeta, line)
		}
	case isFrame(line):
		parser.currentTrace = append(parser.currentTrace, line)
	default:
		parser.observer.Diagnostic(line)
		parser.partial = parser.partial || cutsStack(line)
	}

	return nil
}

func (parser *Parser) flush(
	ctx context.Context,
	samples chan<- *collector.Sample,
	scanError error,
) error {
	pendingTraceLines, pendingMetaLines := len(parser.currentTrace), len(parser.currentMeta)

	if pendingTraceLines > 0 {
		if err := parser.processTrace(ctx, samples); err != nil {
			log.Debug().Err(err).Msg("pending trace dropped")
		}
	}

	log.Debug().
		Int("pending_trace_lines", pendingTraceLines).
		Int("pending_meta_lines", pendingMetaLines).
		Msg("scanner has been closed")

	return scanError
}

func (parser *Parser) processTrace(
	ctx context.Context,
	samples chan<- *collector.Sample,
) error {
	if len(parser.currentTrace) == 0 {
		parser.currentMeta = parser.currentMeta[:0]
		return nil
	}

	defer parser.resetState()

	if parser.partial {
		log.Debug().Int("frames", len(parser.currentTrace)).Msg("partial trace dropped")
		parser.observer.Block(Partial)
		return nil
	}

	foldedStack, entryPoint, foldError := foldTrace(parser.currentTrace, parser.keepEntrypointName)
	if foldError != nil {
		log.Debug().
			Err(foldError).
			Str("trace", strings.Join(parser.currentTrace, "\n")).
			Msg("Failed to fold trace")
		parser.observer.Block(Malformed)
		return nil
	}

	if !parser.epValidator.IsValid(entryPoint) {
		log.Debug().
			Str("entrypoint", entryPoint).
			Msg("Disallowed entrypoint in trace")
		parser.observer.Block(Filtered)
		return nil
	}

	parser.observer.Block(Sampled)

	tags := parser.buildTags(entryPoint)
	sample := &collector.Sample{
		Trace:      foldedStack,
		Tags:       tags,
		Time:       time.Now(),
		SampleRate: parser.sampleRate,
	}
	select {
	case samples <- sample:
	case <-ctx.Done():
		return ctx.Err()
	}

	log.Debug().
		Str("entrypoint", entryPoint).
		Str("tags", tags).
		Msg("queued parsed sample")
	log.Trace().
		Str("folded_stack", foldedStack).
		Msg("Trace processed")

	return nil
}

// buildTags lists the static tags, then the dynamic ones, then the entry point: the order
// Pyroscope shows the label set in.
func (parser *Parser) buildTags(entryPoint string) string {
	tags := parser.staticTags

	if dynamicTags := metaToTags(parser.currentMeta, parser.tagsMapping, parser.duplicateKeys); dynamicTags != "" {
		tags = joinTags(tags, dynamicTags)
	}

	if parser.tagEntrypoint {
		tags = joinTags(tags, "entrypoint="+tag.SanitizeValue(entryPoint))
	}

	return tags
}

func joinTags(tags, more string) string {
	if tags == "" {
		return more
	}

	return tags + "," + more
}

func (parser *Parser) resetState() {
	parser.currentTrace = parser.currentTrace[:0]
	parser.currentMeta = parser.currentMeta[:0]
	parser.partial = false
}
