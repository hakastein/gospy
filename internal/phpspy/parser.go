package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"gospy/internal/validator"
	"strings"
)

type Parser struct {
	entryPoints        []string
	tagsMapping        map[string]string
	tagEntrypoint      bool
	keepEntrypointName bool
}

func NewParser(
	entryPoints []string,
	tagsMapping map[string]string,
	tagEntrypoint bool,
	keepEntrypointName bool,
) *Parser {
	return &Parser{
		entryPoints:        entryPoints,
		tagsMapping:        tagsMapping,
		tagEntrypoint:      tagEntrypoint,
		keepEntrypointName: keepEntrypointName,
	}
}

func (prsr *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	foldedStacks chan<- [2]string,
) {
	var (
		currentTrace []string
		currentMeta  []string
		tags         string
		sample       string
		entryPoint   string
		convertError error
		lines        = make(chan string, 1000) // @TODO make it configurable
	)

	// @TODO make it configurable
	epValidator := validator.NewEntryPointValidator(prsr.entryPoints, 1000)

	// Goroutine to read lines from scanner
	go func() {
		defer close(lines)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			log.Error().Err(err).Msg("stdout scan error")
		}
	}()

	// Main loop to process lines and handle context cancellation
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				log.Debug().Msg("folded stacks channel have been closed")
				return
			}

			// Process the line
			if strings.TrimSpace(line) == "" {
				sample, entryPoint, convertError = tracesToFoldedStacks(currentTrace, prsr.keepEntrypointName)

				if convertError == nil {

					if epValidator.IsValid(entryPoint) {
						tags = parseMeta(currentMeta, prsr.tagsMapping)

						if prsr.tagEntrypoint {
							if tags != "" {
								tags += ","
							}
							tags += "entrypoint=" + entryPoint
						}

						foldedStacks <- [2]string{sample, tags}
						log.Trace().
							Str("sample", sample).
							Msg("sample collected")
					} else {
						log.Debug().
							Str("entrypoint", entryPoint).
							Msg("trace entrypoint not allowed")
					}

				} else {
					log.Debug().
						Err(convertError).
						Str("sample", strings.Join(currentTrace, "\n")).
						Msg("unable convert trace to folded stack format")
				}

				currentTrace = nil
				currentMeta = nil

				continue
			}

			if strings.HasPrefix(line, "#") {
				currentMeta = append(currentMeta, line)

				continue
			}

			currentTrace = append(currentTrace, line)
		}
	}
}

// tracesToFoldedStacks returns trace in folded stack format and entrypoint of trace if trace is valid
func tracesToFoldedStacks(trace []string, keepEntrypointName bool) (string, string, error) {
	if len(trace) <= 1 {
		return "", "", errors.New("trace too small")
	}

	var (
		foldedStack strings.Builder
		entryPoint  string
		fileInfo    string
		colonIndex  int
	)

	for i := len(trace) - 1; i >= 0; i-- {
		tokens := strings.Fields(trace[i])
		// 0 - number of trace
		// 1 - function
		// 2 - path with line number
		if len(tokens) < 3 {
			return "", "", errors.New("invalid trace line structure")
		}

		foldedStack.WriteString(tokens[1])

		// last line in trace is entrypoint
		if i == len(trace)-1 {
			fileInfo = tokens[2]
			colonIndex = strings.LastIndex(fileInfo, ":")
			if colonIndex == -1 {
				return "", "", errors.New("invalid file info in trace")
			}

			entryPoint = fileInfo[:colonIndex]

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

// parseMeta extracts dynamic tags from phpspy output
func parseMeta(lines []string, tagsMapping map[string]string) string {
	var (
		tags   strings.Builder
		key    string
		exists bool
	)

	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		keyVal := strings.SplitN(line, " = ", 2)
		if len(keyVal) != 2 {
			continue
		}

		key, exists = tagsMapping[keyVal[0]]

		if !exists {
			continue
		}

		tags.WriteString(fmt.Sprintf("%s=%s", key, keyVal[1]))
		tags.WriteString(",")
	}

	return strings.TrimSuffix(tags.String(), ",")
}
