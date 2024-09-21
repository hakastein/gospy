package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"path/filepath"
	"strings"
)

type Parser struct {
	entryPoints map[string]struct{}
	tagsMapping map[string]string
}

func NewParser(
	entryPoints map[string]struct{},
	tagsMapping map[string]string,
) *Parser {
	return &Parser{
		entryPoints: entryPoints,
		tagsMapping: tagsMapping,
	}
}

func (prsr *Parser) Parse(
	ctx context.Context,
	scanner *bufio.Scanner,
	foldedStacks chan<- [2]string,
) {
	var currentTrace, currentMeta []string

	lines := make(chan string, 1000)

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
				smpl, err := getSampleFromTrace(currentTrace, prsr.entryPoints)
				if err == nil {
					tags := parseMeta(currentMeta, prsr.tagsMapping)
					foldedStacks <- [2]string{smpl, tags}
					log.Trace().
						Str("sample", smpl).
						Msg("sample collected")
				} else {
					log.Debug().
						Err(err).
						Str("sample", strings.Join(currentTrace, "\n")).
						Msg("unable to get smpl from trace")
				}
				currentTrace = nil
				currentMeta = nil
			} else if line[0] == '#' {
				currentMeta = append(currentMeta, line)
			} else {
				currentTrace = append(currentTrace, line)
			}
		}
	}
}

func getSampleFromTrace(
	trace []string, entryPoints map[string]struct{},
) (string, error) {
	if len(trace) < 2 {
		return "", errors.New("trace too small")
	}

	fields := strings.Fields(trace[len(trace)-1])
	if len(fields) != 3 {
		return "", errors.New("invalid trace line structure")
	}

	fileName := filepath.Base(strings.Split(fields[2], ":")[0])
	if len(entryPoints) > 0 {
		if _, exists := entryPoints[fileName]; !exists {
			return "", fmt.Errorf("trace entrypoint '%s' not in list", fileName)
		}
	}

	return makeSample(trace, fileName)
}

// parseMeta extracts dynamic tags from phpspy output
func parseMeta(lines []string, tagsMapping map[string]string) string {
	var tags strings.Builder
	first := true

	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		keyVal := strings.SplitN(line, " = ", 2)
		if len(keyVal) != 2 {
			continue
		}

		key, exists := tagsMapping[keyVal[0]]

		if !exists {
			continue
		}

		if !first {
			tags.WriteString(",")
		}

		tags.WriteString(fmt.Sprintf("%s=%s", key, keyVal[1]))
		first = false
	}

	return tags.String()
}

// makeSample constructs a sample string from a trace
func makeSample(sampleArr []string, fileName string) (string, error) {
	var smpl strings.Builder
	for i := len(sampleArr) - 1; i >= 0; i-- {
		fields := strings.Fields(sampleArr[i])
		if len(fields) < 3 {
			return "", errors.New("invalid trace line structure")
		}
		smpl.WriteString(fields[1])
		if i == len(sampleArr)-1 {
			smpl.WriteString(" (" + fileName + ")")
		}
		if i > 0 {
			smpl.WriteString(";")
		}
	}
	return smpl.String(), nil
}
