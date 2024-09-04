package main

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

func parseMeta(str string, tags map[string]string) map[string]string {
	data := make(map[string]string)
	lines := strings.Split(str, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		replacer := strings.NewReplacer(
			"# glopeek ", "",
			"# peek ", "",
			"# ", "",
		)

		line = replacer.Replace(line)

		keyval := strings.Split(line, " = ")

		if len(keyval) != 2 {
			continue
		}

		if key, exists := tags[keyval[0]]; exists {
			data[key] = keyval[1]
		}
	}

	return data
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
		var currentTrace strings.Builder
		var currentMeta strings.Builder

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
					collection.addSample(
						currentTrace.String(),
						parseMeta(currentMeta.String(), tags),
					)
				case line[0] == '#':
					currentMeta.WriteString(line + "\n")
				default:
					currentTrace.WriteString(line + "\n")
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
