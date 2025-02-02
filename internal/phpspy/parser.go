package phpspy

import (
	"bufio"
	"context"
	"github.com/hakastein/gospy/internal/collector"
	"github.com/hakastein/gospy/internal/tag"
	"github.com/hakastein/gospy/internal/transform"
	lru "github.com/hashicorp/golang-lru"
	"strings"
	"time"

	"github.com/hakastein/gospy/internal/validator"
	"github.com/rs/zerolog/log"
)

const (
	entryPointValidatorCacheSize = 1000
	traceCapacity                = 100
)

type Parser struct {
	entryPoints        []string
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
	cache, err := lru.New(entryPointValidatorCacheSize)
	if err != nil {
		panic("failed to create LRU cache: " + err.Error())
	}

	return &Parser{
		entryPoints:        entryPoints,
		tagsMapping:        tagsMapping,
		tagEntrypoint:      tagEntrypoint,
		keepEntrypointName: keepEntrypointName,
		currentTrace:       make([]string, 0, traceCapacity),
		currentMeta:        make([]string, 0, len(tagsMapping)),
		epValidator:        validator.New(entryPoints, cache),
	}
}

// Parse reads and processes lines from the scanner, converting them into folded stack samples.
func (parser *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	foldedStacks chan<- *collector.Sample,
) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					log.Error().Err(err).Msg("Error reading from stdout")
				}
				log.Debug().Msg("Scanner has been closed")
				return
			}

			line := scanner.Text()

			if trimmed := strings.TrimSpace(line); trimmed == "" {
				parser.processTrace(foldedStacks)
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

// processTrace converts the current trace to a folded stack and sends it to the foldedStacks channel.
func (parser *Parser) processTrace(
	foldedStacks chan<- *collector.Sample,
) {
	defer parser.resetState()

	if len(parser.currentTrace) == 0 {
		return
	}

	sample, entryPoint, convertError := transform.TracesToFoldedStacks(parser.currentTrace, parser.keepEntrypointName)
	if convertError != nil {
		log.Debug().
			Err(convertError).
			Str("sample", strings.Join(parser.currentTrace, "\n")).
			Msg("Failed to convert trace")
		return
	}

	if !parser.epValidator.IsValid(entryPoint) {
		log.Debug().
			Str("entrypoint", entryPoint).
			Msg("Disallowed entrypoint in trace")
		return
	}

	parser.buildTags(entryPoint)
	foldedStacks <- &collector.Sample{Trace: sample, Tags: parser.tags.String(), Time: time.Now()}
	log.Trace().
		Str("sample", sample).
		Msg("Trace processed")
}

// buildTags constructs the tags string based on metadata and entry point.
func (parser *Parser) buildTags(entryPoint string) {
	parsedTags := transform.MetaToTags(parser.currentMeta, parser.tagsMapping)
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
