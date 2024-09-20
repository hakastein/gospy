package phpspy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Profiler implementation of profiler.Profiler
type Profiler struct {
	executable string
	args       []string
	cmd        *exec.Cmd
	mu         sync.Mutex
}

func NewProfiler(
	executable string,
	args []string,
) *Profiler {
	return &Profiler{
		executable: executable,
		args:       args,
	}
}

func (prflr *Profiler) Start(ctx context.Context) (*bufio.Scanner, error) {
	prflr.mu.Lock()
	defer prflr.mu.Unlock()

	cmd := exec.CommandContext(ctx, prflr.executable, prflr.args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	prflr.cmd = cmd
	scanner := bufio.NewScanner(stdout)
	return scanner, nil
}

func (prflr *Profiler) Stop() error {
	prflr.mu.Lock()
	defer prflr.mu.Unlock()

	if prflr.cmd == nil {
		return nil
	}

	// Check if process is already exited
	if prflr.cmd.ProcessState != nil && prflr.cmd.ProcessState.Exited() {
		return nil
	}

	err := prflr.cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		return err
	}

	return nil
}

func (prflr *Profiler) Wait() error {
	prflr.mu.Lock()
	defer prflr.mu.Unlock()

	if prflr.cmd == nil {
		return errors.New("no command to wait for")
	}

	return prflr.cmd.Wait()
}

func (prflr *Profiler) IsSupportable() (bool, error) {
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
		if extractFlagValue[bool](prflr.args, keys.longKey, keys.shortKey, false) {
			return false, fmt.Errorf("flag -%s/--%s is unsupported by gospy", keys.shortKey, keys.longKey)
		}
	}

	output := extractFlagValue[string](prflr.args, "output", "o", "stdout")
	if output != "stdout" && output != "-" {
		return false, errors.New("output must be set to stdout")
	}

	pgrepMode := extractFlagValue[string](prflr.args, "pgrep", "P", "")
	if pgrepMode != "" {
		bufferSize := extractFlagValue[int](prflr.args, "buffer-size", "b", 4096)
		eventHandlerOpts := extractFlagValue[string](prflr.args, "event-handler-opts", "J", "")
		if bufferSize > 4096 && !strings.Contains(eventHandlerOpts, "m") {
			log.Warn().Msg("using large buffer size without mutex; consider adding -J m with -b > 4096")
		}
	}
	return true, nil
}

func (prflr *Profiler) GetHZ() int {
	return extractFlagValue[int](prflr.args, "rate-hz", "H", 99)
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
