package main

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func parseMeta(line string, tags map[string]string) (string, bool) {
	line = strings.TrimSpace(line)

	replacer := strings.NewReplacer(
		"# glopeek ", "",
		"# peek ", "",
		"# ", "",
	)

	line = replacer.Replace(line)

	keyval := strings.Split(line, " = ")

	if key, exists := tags[keyval[0]]; exists && len(keyval) == 2 {
		return fmt.Sprintf("%s=%s", key, keyval[1]), true
	}

	return "", false
}

func makeSample(sampleArr []string) string {
	lastChar := len(sampleArr) - 1
	var sample strings.Builder
	for i := lastChar; i >= 0; i -= 1 {
		strArr := strings.Split(sampleArr[i], " ")

		sample.WriteString(strArr[1])
		if i == lastChar {
			fileName := filepath.Base(strings.Split(strArr[2], ":")[0])
			sample.WriteString(" (")
			sample.WriteString(fileName)
			sample.WriteString(")")
		}
		if i != 1 {
			sample.WriteString(";")
		}
	}

	return sample.String()
}

func makeTags(tagsArr []string) string {
	return strings.Join(tagsArr, ",")
}

// runPhpspy запускает phpspy и обрабатывает его вывод
func runPhpspy(channel chan SampleCollection, args []string, tags map[string]string, interval time.Duration) error {
	for {
		cmd := exec.Command("phpspy", args...)
		stdout, err := cmd.StdoutPipe()

		if err != nil {
			return fmt.Errorf("ошибка получения stdout: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("ошибка получения stdout: %w", err)
		}

		scanner := bufio.NewScanner(stdout)
		collection := newSampleCollection()

		var currentTrace []string
		var currentTags []string

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		go func() {
			for range ticker.C {
				collection.to = time.Now()
				channel <- *collection
				collection = newSampleCollection()
			}
		}()

		go func() {
			for scanner.Scan() {
				line := scanner.Text()

				switch {
				case len(strings.TrimSpace(line)) == 0:
					collection.addSample(makeSample(currentTrace), makeTags(currentTags))
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
		}()

		if err := cmd.Wait(); err != nil {
			log.Printf("phpspy завершился с ошибкой: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		return nil
	}
}
