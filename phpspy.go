package main

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

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

func makeSample(sampleArr []string) string {
	var sample strings.Builder
	for i := len(sampleArr) - 1; i >= 0; i-- {
		fields := strings.Fields(sampleArr[i])
		if len(fields) < 3 {
			continue
		}
		sample.WriteString(fields[1])
		if i == len(sampleArr)-1 {
			fileName := filepath.Base(strings.Split(fields[2], ":")[0])
			sample.WriteString(" (" + fileName + ")")
		}
		if i > 0 {
			sample.WriteString(";")
		}
	}
	return sample.String()
}

func makeTags(tagsArr []string) string {
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

func runPhpspy(channel chan *SampleCollection, args []string, tags map[string]string, interval time.Duration, logger *zap.Logger) error {

	for _, keys := range [][2]string{
		{"version", "v"},
		{"top", "t"},
		{"help", "h"},
		{"single-line", "1"},
	} {
		if extractFlagValue[bool](args, keys[0], keys[1], false) {
			return fmt.Errorf("-%s, --%s flag of phpspy is unsupported by gospy", keys[1], keys[0])
		}
	}

	output := extractFlagValue[string](args, "output", "o", "stdout")
	if output != "stdout" && output != "-" {
		return errors.New("output must be set to stdout")
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

	cmd := exec.Command("phpspy", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("phpspy stdout error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("phpspy start error: %w", err)
	}

	logger.Info("phpspy started")

	scanner := bufio.NewScanner(stdout)
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

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(currentTrace) > 0 {
				sample := makeSample(currentTrace)
				collection.addSample(sample, makeTags(currentTags))
				sampleCount++
				logger.Debug("phpspy collected sample", zap.String("sample", sample))
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
		return fmt.Errorf("error reading phpspy output: %s", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("phpspy exited with error: %s", err)
	}

	logger.Info("phpspy exited successfully")
	return nil
}
