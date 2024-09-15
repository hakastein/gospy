package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"gospy/internal/sample"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Profiler implementation of profiler.Profiler
type Profiler struct {
	executable  string
	args        []string
	cmd         *exec.Cmd
	mu          sync.Mutex
	rateHz      int
	interval    time.Duration
	entryPoints map[string]struct{}
	tags        map[string]string
}

func NewProfiler(
	executable string,
	args []string,
	interval time.Duration,
	entryPoints map[string]struct{},
	tags map[string]string,
) (*Profiler, error) {
	rateHz, phpspyArgsErr := parsePhpSpyArguments(args)
	if phpspyArgsErr != nil {
		return nil, phpspyArgsErr
	}

	return &Profiler{
		executable:  executable,
		args:        args,
		rateHz:      rateHz,
		interval:    interval,
		entryPoints: entryPoints,
		tags:        tags,
	}, nil
}

func (p *Profiler) Start(ctx context.Context) (*bufio.Scanner, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cmd := exec.CommandContext(ctx, p.executable, p.args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p.cmd = cmd
	scanner := bufio.NewScanner(stdout)
	return scanner, nil
}

func (p *Profiler) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil {
		return nil
	}

	// Check if process is already exited
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}

	err := p.cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			// Process already finished; no need to log an error
			return nil
		}
		log.Info().Err(err).Msg("failed to terminate process")
		killErr := p.cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			log.Info().Err(killErr).Msg("failed to kill process")
		}
		return err
	}

	return nil
}

func (p *Profiler) Wait() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil {
		return errors.New("no command to wait for")
	}

	return p.cmd.Wait()
}

func (p *Profiler) ParseOutput(
	ctx context.Context,
	cancel context.CancelFunc,
	scanner *bufio.Scanner,
	samplesChannel chan<- *sample.Collection,
) {
	scanPhpSpyStdout(ctx, cancel, scanner, samplesChannel, p.rateHz, p.interval, p.entryPoints, p.tags)
}

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
		flag := flags[i]
		switch {
		case strings.HasPrefix(flag, longKey+"="):
			return convertTo[T](strings.TrimPrefix(flag, longKey+"="))
		case flag == shortKey && i+1 < flagLen:
			return convertTo[T](flags[i+1])
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

func recoverAndLogPanic(message string, cancel context.CancelFunc) {
	if r := recover(); r != nil {
		if err, ok := r.(error); ok {
			log.Error().Err(err).Msg(message)
		} else {
			log.Error().Interface("error", r).Msg(message)
		}
		cancel()
	}
}

func scanPhpSpyStdout(
	ctx context.Context,
	cancel context.CancelFunc,
	scanner *bufio.Scanner,
	samplesChannel chan<- *sample.Collection,
	rateHz int,
	interval time.Duration,
	entryPoints map[string]struct{},
	tags map[string]string,
) {
	defer recoverAndLogPanic("panic recovered in scanPhpSpyStdout", cancel)

	collection := sample.NewCollection(rateHz)
	sampleCount := 0

	var currentTrace, currentTags []string

	lines := make(chan string)

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

	// Ticker to handle sample collection intervals
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Main loop to process lines and handle context cancellation
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if sampleCount > 0 {
				collection.Finish()
				select {
				case samplesChannel <- collection:
					// Successfully sent the collection
				case <-ctx.Done():
					return
				}
				log.Info().Int("count", sampleCount).Msg("samples collected")
				collection = sample.NewCollection(rateHz)
				sampleCount = 0
			}
		case line, ok := <-lines:
			if !ok {
				// Scanner has finished
				if sampleCount > 0 {
					collection.Finish()
					select {
					case samplesChannel <- collection:
						// Successfully sent the collection
					case <-ctx.Done():
						return
					}
					log.Info().Int("count", sampleCount).Msg("samples collected")
				}
				return
			}
			// Process the line
			if strings.TrimSpace(line) == "" {
				smpl, err := getSampleFromTrace(currentTrace, entryPoints)
				if err == nil {
					collection.AddSample(smpl, makeTags(currentTags))
					sampleCount++
				} else {
					log.Debug().Err(err).Msg("unable to get smpl from trace")
				}
				currentTrace = nil
				currentTags = nil
			} else if line[0] == '#' {
				if tag, exists := parseMeta(line, tags); exists {
					currentTags = append(currentTags, tag)
				}
			} else {
				currentTrace = append(currentTrace, line)
			}
		}
	}
}

func parsePhpSpyArguments(args []string) (int, error) {
	unsupportedFlags := []struct {
		longKey  string
		shortKey string
	}{
		{"version", "v"},
		{"top", "t"},
		{"help", "h"},
		{"single-line", "1"},
	}
	for _, keys := range unsupportedFlags {
		if extractFlagValue[bool](args, keys.longKey, keys.shortKey, false) {
			return -1, fmt.Errorf("flag -%s/--%s is unsupported by gospy", keys.shortKey, keys.longKey)
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
			log.Warn().Msg("using large buffer size without mutex; consider adding -J m with -b > 4096")
		}
	}

	rateHz := extractFlagValue[int](args, "rate-hz", "H", 99)
	return rateHz, nil
}
