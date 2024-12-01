package phpspy

import (
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gospy/internal/types"
)

// processTrace converts the current trace to a folded stack and sends it to the foldedStacks channel.
func (parser *Parser) processTrace(
	foldedStacks chan<- *types.Sample,
) {
	defer parser.resetState()

	if len(parser.currentTrace) == 0 {
		return
	}

	sample, entryPoint, convertError := tracesToFoldedStacks(parser.currentTrace, parser.keepEntrypointName)
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
	foldedStacks <- &types.Sample{Trace: sample, Tags: parser.tags.String(), Time: time.Now()}
	log.Trace().
		Str("sample", sample).
		Msg("Trace processed")
}

// buildTags constructs the tags string based on metadata and entry point.
func (parser *Parser) buildTags(entryPoint string) {
	parsedTags := parseMeta(parser.currentMeta, parser.tagsMapping)
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

// tracesToFoldedStacks converts trace lines to folded stack format and extracts the entry point.
func tracesToFoldedStacks(trace []string, keepEntrypointName bool) (string, string, error) {
	if len(trace) < 2 {
		return "", "", errors.New("trace insufficient length")
	}

	var (
		foldedStack strings.Builder
		entryPoint  string
	)

	lastIndex := len(trace) - 1
	for i := lastIndex; i >= 0; i-- {
		tokens := strings.Fields(trace[i])
		// 0 - number of trace
		// 1 - function
		// 2 - path with line number
		if len(tokens) < 3 {
			return "", "", errors.New("invalid trace format")
		}

		foldedStack.WriteString(tokens[1])

		// Last line in trace is entry point
		if i == lastIndex {
			fileInfo := tokens[2]
			colonIdx := strings.LastIndex(fileInfo, ":")
			if colonIdx == -1 {
				return "", "", errors.New("invalid file info in trace")
			}
			entryPoint = fileInfo[:colonIdx]
			if keepEntrypointName {
				foldedStack.WriteString(" ")
				foldedStack.WriteString(entryPoint)
			}
		}

		if i > 0 {
			foldedStack.WriteString(";")
		}
	}

	return foldedStack.String(), entryPoint, nil
}
