package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// parseMeta обрабатывает строки метаданных и сопоставляет их с тегами
func parseMeta(line string, tags map[string]string) (string, bool) {
	line = strings.TrimPrefix(line, "# glopeek ")
	line = strings.TrimPrefix(line, "# peek ")
	line = strings.TrimPrefix(line, "# ")

	keyval := strings.SplitN(line, " = ", 2)
	if len(keyval) != 2 {
		return "", false
	}

	if key, exists := tags[keyval[0]]; exists {
		return fmt.Sprintf("%s=%s", key, keyval[1]), true
	}

	return "", false
}

// makeSample создает строку-сэмпл на основе переданного трейсинга
func makeSample(sampleArr []string) string {
	var sample strings.Builder
	lastChar := len(sampleArr) - 1

	for i := lastChar; i >= 0; i-- {
		strArr := strings.Fields(sampleArr[i])
		if len(strArr) < 3 {
			continue
		}

		sample.WriteString(strArr[1])
		if i == lastChar {
			fileName := filepath.Base(strings.Split(strArr[2], ":")[0])
			sample.WriteString(" (")
			sample.WriteString(fileName)
			sample.WriteString(")")
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

// extractFlagValue извлекает значение флага из списка флагов
func extractFlagValue[T any](flags *[]string, longKey string, shortKey string, defaultValue T) T {
	var value T
	var found bool
	missedFlags := []string{}
	flaglen := len(*flags)

	shortKey = "-" + shortKey
	longKey = "--" + longKey

	for i := 0; i < flaglen; i++ {
		flag := (*flags)[i]
		if strings.HasPrefix(flag, longKey+"=") {
			value = convertTo[T](strings.TrimPrefix(flag, longKey+"="))
			found = true
		} else if flag == shortKey && i+1 < flaglen {
			value = convertTo[T]((*flags)[i+1])
			found = true
			i++ // пропускаем следующий элемент
		} else if flag == longKey || flag == shortKey {
			if _, ok := any(value).(bool); ok {
				value = convertTo[T]("true")
				found = true
			}
		} else {
			missedFlags = append(missedFlags, flag)
		}
	}

	*flags = missedFlags
	if !found {
		return defaultValue
	}
	return value
}

// convertTo преобразует строку в тип T
func convertTo[T any](value string) T {
	var result T
	switch any(result).(type) {
	case string:
		result = any(value).(T)
	case int:
		if intValue, err := strconv.Atoi(value); err == nil {
			result = any(intValue).(T)
		}
	case bool:
		if boolValue, err := strconv.ParseBool(value); err == nil {
			result = any(boolValue).(T)
		}
	}
	return result
}

// runPhpspy запускает phpspy и обрабатывает его вывод
func runPhpspy(channel chan *SampleCollection, args []string, tags map[string]string, interval time.Duration) error {
	for {
		argsCopy := make([]string, len(args))
		copy(argsCopy, args)

		// Извлечение значений флагов
		rateHz := extractFlagValue[int](&argsCopy, "rate-hz", "H", 99)

		isTop := extractFlagValue[bool](&argsCopy, "top", "t", false)
		isHelp := extractFlagValue[bool](&argsCopy, "help", "h", false)
		isVersion := extractFlagValue[bool](&argsCopy, "version", "v", false)
		output := extractFlagValue[string](&argsCopy, "output", "o", "stdout")
		isSingleLine := extractFlagValue[bool](&argsCopy, "single-line", "1", false)

		if isTop {
			return errors.New("-t, --top flag of phpspy is unsupported by gospy")
		}

		if isHelp {
			return errors.New("-h, --help flag of phpspy is unsupported by gospy")
		}

		if isSingleLine {
			return errors.New("-v, --version flag of phpspy is unsupported by gospy")
		}

		if isVersion {
			return errors.New("-1, --signle-line flag of phpspy is unsupported by gospy")
		}

		if output != "stdout" && output != "-" {
			return errors.New("output must be set in stdout")
		}

		cmd := exec.Command("phpspy", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("phpspy stdout error: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("phpspy start error: %w", err)
		}

		scanner := bufio.NewScanner(stdout)
		collection := newSampleCollection(rateHz)

		var currentTrace []string
		var currentTags []string

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		go func() {
			for range ticker.C {
				collection.to = time.Now()
				channel <- collection
				collection = newSampleCollection(rateHz)
			}
		}()

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.TrimSpace(line) == "":
				if len(currentTrace) > 0 {
					collection.addSample(makeSample(currentTrace), makeTags(currentTags))
				}
				currentTags = nil
				currentTrace = nil
			case line[0] == '#':
				tag, exists := parseMeta(line, tags)
				if exists {
					currentTags = append(currentTags, tag)
				}
			default:
				currentTrace = append(currentTrace, line)
			}
		}

		if err := cmd.Wait(); err != nil {
			log.Printf("phpspy exited with: %v", err)
			time.Sleep(time.Second)
			continue
		}

		return nil
	}
}
