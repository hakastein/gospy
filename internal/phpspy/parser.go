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

const traceCapacity = 100

type Parser struct {
	tagsMapping        map[string][]tag.DynamicTag
	tagEntrypoint      bool
	keepEntrypointName bool
	currentTrace       []string
	currentMeta        []string
	tags               strings.Builder
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
		currentMeta:        make([]string, 0, len(tagsMapping)),
		epValidator:        validator.New(entryPoints),
	}
}

// Parse does not close samples; the caller owns closing it.
func (parser *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	samples chan<- *collector.Sample,
) {
	for {
		select {
		case <-ctx.Done():
			log.Debug().Msg("parser stopped due to context cancellation")
			return
		default:
			if !scanner.Scan() {
				if len(parser.currentTrace) > 0 {
					if !parser.processTrace(ctx, samples) {
						return
					}
				}
				if err := scanner.Err(); err != nil {
					log.Error().Err(err).Msg("Error reading from stdout")
				}
				log.Debug().
					Int("pending_trace_lines", len(parser.currentTrace)).
					Int("pending_meta_lines", len(parser.currentMeta)).
					Msg("scanner has been closed")
				return
			}

			line := scanner.Text()
			log.Trace().Str("line", line).Msg("read profiler output line")

			if trimmed := strings.TrimSpace(line); trimmed == "" {
				if !parser.processTrace(ctx, samples) {
					return
				}
				continue
			}

			if strings.HasPrefix(line, "#") {
				parser.addToMeta(line)
				continue
			}

			parser.addToTrace(line)
		}
	}
}

func (parser *Parser) addToTrace(line string) {
	parser.currentTrace = append(parser.currentTrace, line)
}

func (parser *Parser) addToMeta(line string) {
	parser.currentMeta = append(parser.currentMeta, line)
}

func (parser *Parser) processTrace(
	ctx context.Context,
	samples chan<- *collector.Sample,
) bool {
	defer parser.resetState()

	if len(parser.currentTrace) == 0 {
		return true
	}

	foldedStack, entryPoint, foldError := foldTrace(parser.currentTrace, parser.keepEntrypointName)
	if foldError != nil {
		log.Debug().
			Err(foldError).
			Str("trace", strings.Join(parser.currentTrace, "\n")).
			Msg("Failed to fold trace")
		return true
	}

	if !parser.epValidator.IsValid(entryPoint) {
		log.Debug().
			Str("entrypoint", entryPoint).
			Msg("Disallowed entrypoint in trace")
		return true
	}

	parser.buildTags(entryPoint)
	select {
	case samples <- &collector.Sample{Trace: foldedStack, Tags: parser.tags.String(), Time: time.Now()}:
	case <-ctx.Done():
		return false
	}
	log.Debug().
		Str("entrypoint", entryPoint).
		Str("tags", parser.tags.String()).
		Msg("queued parsed sample")
	log.Trace().
		Str("folded_stack", foldedStack).
		Msg("Trace processed")
	return true
}

// buildTags constructs the tags string based on metadata and entry point.
func (parser *Parser) buildTags(entryPoint string) {
	parsedTags := metaToTags(parser.currentMeta, parser.tagsMapping)
	parser.tags.WriteString(parsedTags)
	if parser.tagEntrypoint {
		if parsedTags != "" {
			parser.tags.WriteRune(',')
		}
		parser.tags.WriteString("entrypoint=")
		parser.tags.WriteString(entryPoint)
	}
}

// resetState clears the current trace, metadata, and tags for the next parsing session.
func (parser *Parser) resetState() {
	parser.currentTrace = parser.currentTrace[:0]
	parser.currentMeta = parser.currentMeta[:0]
	parser.tags.Reset()
}
