package main

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// parseMeta extracts dynamic tags from phpspy output
func parseMeta(line string, tags map[string]string) (string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "# glopeek "), "# peek "), "# "))
	keyVal := strings.SplitN(line, " = ", 2)
	if len(keyVal) != 2 {
		return "", false
	}
	if key, exists := tags[keyVal[0]]; exists {
		return fmt.Sprintf("%s=%s", key, keyVal[1]), true
	}
	return "", false
}

// makeSample constructs a sample string from a trace
func makeSample(sampleArr []string, fileName string) (string, error) {
	var sample strings.Builder
	for i := len(sampleArr) - 1; i >= 0; i-- {
		fields := strings.Fields(sampleArr[i])
		if len(fields) < 3 {
			return "", errors.New("invalid trace line structure")
		}
		sample.WriteString(fields[1])
		if i == len(sampleArr)-1 {
			sample.WriteString(" (" + fileName + ")")
		}
		if i > 0 {
			sample.WriteString(";")
		}
	}
	return sample.String(), nil
}

func makeTags(tagsArr []string) string {
	if len(tagsArr) == 0 {
		return ""
	}
	return strings.Join(tagsArr, ",")
}

func extractFlagValue[T any](flags []string, longKey, shortKey string, defaultValue T) T {
	shortKey, longKey = "-"+shortKey, "--"+longKey
	flagLen := len(flags)

	for i := 0; i < flagLen; i++ {
		flag := (flags)[i]
		switch {
		case strings.HasPrefix(flag, longKey+"="):
			return convertTo[T](strings.TrimPrefix(flag, longKey+"="))
		case flag == shortKey && i+1 < flagLen:
			return convertTo[T]((flags)[i+1])
		case flag == longKey || flag == shortKey:
			if _, ok := any(defaultValue).(bool); ok {
				return convertTo[T]("true")
			}
		}
	}

	return defaultValue
}

func convertTo[T any](value string) T {
	var result T
	switch any(result).(type) {
	case string:
		return any(value).(T)
	case int:
		if intValue, err := strconv.Atoi(value); err == nil {
			return any(intValue).(T)
		}
	case bool:
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return any(boolValue).(T)
		}
	}
	return result
}

func getSampleFromTrace(trace []string, entryPoints map[string]struct{}) (string, error) {
	if len(trace) < 2 {
		return "", errors.New("trace too small")
	}

	fields := strings.Fields(trace[len(trace)-1])
	if len(fields) != 3 {
		return "", errors.New("incorrect trace format")
	}

	fileName := filepath.Base(strings.Split(fields[2], ":")[0])
	if len(entryPoints) > 0 {
		if _, exists := entryPoints[fileName]; !exists {
			return "", fmt.Errorf("trace entrypoint '%s' not in list", fileName)
		}
	}

	return makeSample(trace, fileName)
}

func scanPhpSpyStdout(
	scannerChannel chan *bufio.Scanner,
	channel chan *SampleCollection,
	rateHz int,
	interval time.Duration,
	entryPoints map[string]bool,
	tags map[string]string,
	logger *zap.Logger,
) {

	collection := newSampleCollection(rateHz)
	sampleCount := 0

	var currentTrace, currentTags []string
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			if sampleCount > 0 {
				collection.until = time.Now()
				channel <- collection
				collection = newSampleCollection(rateHz)
				logger.Info("phpspy samples collected", zap.Int("count", sampleCount))
				sampleCount = 0
			}
		}
	}()

	for scanner := range scannerChannel {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				// ignoring traces that 1 line length as meaningless
				// @TODO make flag
				sample, sampleError := getSampleFromTrace(currentTrace, entryPoints)
				if sampleError == nil {
					collection.addSample(sample, makeTags(currentTags))
					sampleCount++
				} else {
					// sampler produces reasonable number of trash lines, ignore them
					logger.Debug("unable to get sample from trace", zap.Error(sampleError))
				}
				currentTrace, currentTags = nil, nil
			} else if line[0] == '#' {
				if tag, exists := parseMeta(line, tags); exists {
					currentTags = append(currentTags, tag)
				}
			} else {
				currentTrace = append(currentTrace, line)
			}
		}

		if err := scanner.Err(); err != nil {
			logger.Error("stdout scan error:", zap.Error(err))
		}
	}
}

func parsePhpSpyArguments(args []string, logger *zap.Logger) (int, error) {
	for _, keys := range [][2]string{
		{"version", "v"},
		{"top", "t"},
		{"help", "h"},
		{"single-line", "1"},
	} {
		if extractFlagValue[bool](args, keys[0], keys[1], false) {
			return -1, fmt.Errorf("-%s, --%s flag of phpspy is unsupported by gospy", keys[1], keys[0])
		}
	}

	output := extractFlagValue[string](args, "output", "o", "stdout")
	if output != "stdout" && output != "-" {
		return -1, errors.New("output must be set to stdout")
	}

	pgrepMode := extractFlagValue[string](args, "pgrep", "P", "")

	if pgrepMode != "" {
		bufferSize := extractFlagValue[int](args, "buffer-size", "b", 4096)
		eventHandlerOpts := extractFlagValue[string](args, "event-handler-opts", "J", "")

		if bufferSize > 4096 && !strings.Contains(eventHandlerOpts, "m") {
			logger.Warn("You use big buffer size without mutex. Consider using -J m with -b greater than 4096")
		}
	}

	rateHz := extractFlagValue[int](args, "rate-hz", "H", 99)

	return rateHz, nil
}
