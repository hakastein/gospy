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

// parseMeta обрабатывает строки метаданных и сопоставляет их с тегами
func parseMeta(line string, tags map[string]string) (string, bool) {
	line = strings.TrimSpace(line)

	// Используем более быстрый strings.Replace для удаления меток
	line = strings.Replace(line, "# glopeek ", "", 1)
	line = strings.Replace(line, "# peek ", "", 1)
	line = strings.Replace(line, "# ", "", 1)

	// Разбиваем строку на ключ-значение
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

	// Создаем сэмпл с конца для упрощения построения строки
	for i := lastChar; i >= 0; i-- {
		strArr := strings.Fields(sampleArr[i]) // strings.Fields быстрее разбивает строку на слова
		if len(strArr) < 3 {
			continue // Пропускаем некорректные строки
		}

		sample.WriteString(strArr[1])

		if i == lastChar {
			// Извлекаем имя файла только для последней строки
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

// makeTags объединяет теги в строку
func makeTags(tagsArr []string) string {
	return strings.Join(tagsArr, ",")
}

// runPhpspy запускает phpspy и обрабатывает его вывод
func runPhpspy(channel chan *SampleCollection, args []string, tags map[string]string, interval time.Duration) error {
	for {
		cmd := exec.Command("phpspy", args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("phpspy stdout error: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("phpspy start error: %w", err)
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
				channel <- collection
				collection = newSampleCollection()
			}
		}()

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case strings.TrimSpace(line) == "":
				// Если пустая строка — завершили текущий трейс
				if len(currentTrace) > 0 {
					collection.addSample(makeSample(currentTrace), makeTags(currentTags))
				}
				currentTags = nil
				currentTrace = nil
			case line[0] == '#':
				// Обрабатываем метаданные
				tag, exists := parseMeta(line, tags)
				if exists {
					currentTags = append(currentTags, tag)
				}
			default:
				// Добавляем строку в текущий трейс
				currentTrace = append(currentTrace, line)
			}
		}

		if err := cmd.Wait(); err != nil {
			log.Printf("phpspy exited with: %v", err)
			time.Sleep(1 * time.Second) // Ожидаем перед перезапуском
			continue
		}

		return nil
	}
}
