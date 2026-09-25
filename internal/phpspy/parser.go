package phpspy

import (
	"bufio"
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/validator"
)

const (
	traceCapacity = 100
	metaCapacity  = 16
)

type Parser struct {
	tagsMapping        map[string][]tag.DynamicTag
	tagEntrypoint      bool
	keepEntrypointName bool
	currentTrace       []string
	currentMeta        []string
	epValidator        *validator.EntryPointValidator
}

// NewParser initializes a new Parser.
func NewParser(
	entryPoints []string,
	tagsMapping map[string][]tag.DynamicTag,
	tagEntrypoint bool,
	keepEntrypointName bool,
) *Parser {
	return &Parser{
		tagsMapping:        tagsMapping,
		tagEntrypoint:      tagEntrypoint,
		keepEntrypointName: keepEntrypointName,
		currentTrace:       make([]string, 0, traceCapacity),
		currentMeta:        make([]string, 0, metaCapacity),
		epValidator:        validator.New(entryPoints),
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
	default:
		parser.currentTrace = append(parser.currentTrace, line)
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
	defer parser.resetState()

	if len(parser.currentTrace) == 0 {
		return nil
	}

	foldedStack, entryPoint, foldError := foldTrace(parser.currentTrace, parser.keepEntrypointName)
	if foldError != nil {
		log.Debug().
			Err(foldError).
			Str("trace", strings.Join(parser.currentTrace, "\n")).
			Msg("Failed to fold trace")
		return nil
	}

	if !parser.epValidator.IsValid(entryPoint) {
		log.Debug().
			Str("entrypoint", entryPoint).
			Msg("Disallowed entrypoint in trace")
		return nil
	}

	tags := parser.buildTags(entryPoint)
	select {
	case samples <- &collector.Sample{Trace: foldedStack, Tags: tags, Time: time.Now()}:
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

func (parser *Parser) buildTags(entryPoint string) string {
	parsedTags := metaToTags(parser.currentMeta, parser.tagsMapping)

	if !parser.tagEntrypoint {
		return parsedTags
	}

	if parsedTags == "" {
		return "entrypoint=" + entryPoint
	}

	return parsedTags + ",entrypoint=" + entryPoint
}

func (parser *Parser) resetState() {
	parser.currentTrace = parser.currentTrace[:0]
	parser.currentMeta = parser.currentMeta[:0]
}
