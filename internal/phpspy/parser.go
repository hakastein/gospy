package phpspy

import (
	"bufio"
	"context"
	"gospy/internal/tag"
	"strings"

	"github.com/rs/zerolog/log"
	"gospy/internal/types"
	"gospy/internal/validator"
)

const (
	entryPointValidatorCacheSize = 1000
	traceCapacity                = 50
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
	return &Parser{
		entryPoints:        entryPoints,
		tagsMapping:        tagsMapping,
		tagEntrypoint:      tagEntrypoint,
		keepEntrypointName: keepEntrypointName,
		currentTrace:       make([]string, 0, traceCapacity),
		currentMeta:        make([]string, 0, len(tagsMapping)),
		epValidator:        validator.NewEntryPointValidator(entryPoints, entryPointValidatorCacheSize),
	}
}

// Parse reads and processes lines from the scanner, converting them into folded stack samples.
func (parser *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	foldedStacks chan<- *types.Sample,
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
