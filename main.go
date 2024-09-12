package main

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

const (
	DefaultRateMB             = 4       // default rate-mb in pyroscope
	DefaultAccumulationPeriod = 10      // period of collecting samples
	Megabyte                  = 1048576 // bytes in megabyte
	RetryCount                = 2       // how many times attempt to resend
)

// Sample - sample with count
type Sample struct {
	sample string
	count  int
}

// SampleCollection - samples grouped by tags
type SampleCollection struct {
	from    time.Time
	until   time.Time
	samples map[string]map[uint64]*Sample
	rateHz  int
	sync.RWMutex
}

func sampleHash(s, tags string) uint64 {
	h := xxhash.Sum64String(s + tags)
	return h
}

func newSampleCollection(rateHz int) *SampleCollection {
	return &SampleCollection{
		from:    time.Now(),
		samples: make(map[string]map[uint64]*Sample),
		rateHz:  rateHz,
	}
}

func (sc *SampleCollection) addSample(str, tags string) {
	sc.Lock()
	defer sc.Unlock()

	hash := sampleHash(str, tags)

	tagSamples, exists := sc.samples[tags]
	if !exists {
		tagSamples = make(map[uint64]*Sample)
		sc.samples[tags] = tagSamples
	}

	if sample, exists := tagSamples[hash]; exists {
		sample.count++
	} else {
		tagSamples[hash] = &Sample{
			sample: str,
			count:  1,
		}
	}
}

// Function for processing tags and separating static and dynamic tags
func getTags(tagsInput []string) (string, map[string]string, error) {
	dynamicTags := make(map[string]string)
	var st strings.Builder
	st.Grow(64) // Preallocate memory for performance

	for _, tag := range tagsInput {
		idx := strings.Index(tag, "=")
		if idx != -1 {
			key := tag[:idx]
			value := tag[idx+1:]
			if len(value) > 1 && value[0] == '%' && value[len(value)-1] == '%' {
				dynamicTags[value[1:len(value)-1]] = key
			} else {
				st.WriteString(tag)
				st.WriteString(",")
			}
		} else {
			return "", dynamicTags, fmt.Errorf("unexpected tag format %s", tag)
		}
	}

	staticTags := st.String()
	if len(staticTags) > 0 {
		staticTags = staticTags[:len(staticTags)-1] // Removing trailing comma
	}

	return staticTags, dynamicTags, nil
}

func mapEntryPoints(entryPoints []string) map[string]bool {
	entryMap := make(map[string]bool, len(entryPoints))

	for _, entry := range entryPoints {
		entryMap[entry] = true
	}

	return entryMap
}

func setupLogger(debug bool) (*zap.Logger, error) {
	var logger *zap.Logger
	var logErr error

	if debug {
		cfg := zap.NewDevelopmentConfig()
		logger, logErr = cfg.Build()
	} else {
		cfg := zap.NewProductionConfig()
		logger, logErr = cfg.Build()
	}

	return logger, logErr
}

func spawn(channel chan *bufio.Scanner, executable string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(executable, args...)

	stdout, stdoutErr := cmd.StdoutPipe()

	if stdoutErr != nil {
		return nil, fmt.Errorf("stdout pipe error: %s", stdoutErr)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	channel <- bufio.NewScanner(stdout)

	return cmd, nil
}

func terminate(cmd *exec.Cmd, logger *zap.Logger) {
	if cmd == nil {
		return
	}

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}

	termErr := cmd.Process.Signal(syscall.SIGTERM)
	if termErr != nil {
		logger.Error("failed to terminate process", zap.Error(termErr))

		err := cmd.Process.Kill()
		if err != nil {
			logger.Error("failed to kill process", zap.Error(err))
		}
	}
}

func handlePanic(cmd *exec.Cmd, logger *zap.Logger) {
	if r := recover(); r != nil {
		var err error
		switch x := r.(type) {
		case string:
			err = errors.New(x)
		case error:
			err = x
		default:
			err = errors.New("unknown panic")
		}
		logger.Error("got panic", zap.Error(err))
		terminate(cmd, logger)
	}
}

func runGoSpy(context *cli.Context) error {
	pyroscopeURL := context.String("pyroscope")
	pyroscopeAuth := context.String("pyroscopeAuth")
	accumulationInterval := context.Duration("accumulation-interval")
	app := context.String("app")
	debug := context.Bool("debug")
	restart := context.String("restart")
	rateMb := context.Int("rate-mb") * Megabyte
	staticTags, dynamicTags, tagsErr := getTags(context.StringSlice("tag"))
	entryPoints := mapEntryPoints(context.StringSlice("entrypoint"))
	arguments := context.Args().Slice()

	if tagsErr != nil {
		return tagsErr
	}

	logger, logErr := setupLogger(debug)
	if logErr != nil {
		return logErr
	}
	defer logger.Sync()

	logger.Info("gospy started",
		zap.String("pyroscope url", pyroscopeURL),
		zap.String("pyroscope auth token", pyroscopeAuth),
		zap.String("static tags", staticTags),
		zap.Duration("phpspy accumulation-interval", accumulationInterval),
	)

	samplesChannel := make(chan *SampleCollection)
	defer close(samplesChannel)
	signalsChannel := make(chan os.Signal, 1)
	defer close(signalsChannel)
	scannerChannel := make(chan *bufio.Scanner)

	signal.Notify(signalsChannel,
		syscall.SIGTERM,
		syscall.SIGINT,
	)

	profilerApp := arguments[0]
	profilerArguments := arguments[1:]

	rateHz, phpspyArgsErr := parsePhpSpyArguments(profilerArguments, logger)
	if phpspyArgsErr != nil {
		return phpspyArgsErr
	}

	var cmd *exec.Cmd

	defer terminate(cmd, logger)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		sig := <-signalsChannel
		if sig == nil {
			return
		}
		logger.Info("signal received", zap.String("signal", sig.String()))
		if err := cmd.Process.Signal(sig); err != nil {
			logger.Error("failed to send signal", zap.String("signal", sig.String()), zap.Error(err))
		}
		os.Exit(1)
	}()

	go func() {
		defer close(scannerChannel)
		defer wg.Done()

		for {
			logger.Info("start phpspy")
			var samplerError error

			cmd, samplerError = spawn(scannerChannel, profilerApp, profilerArguments)

			if samplerError == nil {
				if exitError := cmd.Wait(); exitError != nil {
					samplerError = exitError
				}
			}

			if samplerError != nil {
				logger.Error("error in phpspy", zap.Error(samplerError))
			} else {
				logger.Info("phpspy gracefully stopped")
			}

			switch {
			case restart == "always":
				continue
			case restart == "onerror" && samplerError != nil:
				continue
			case restart == "onsuccess" && samplerError == nil:
				continue
			default:
				return
			}
		}
	}()

	go func() {
		defer handlePanic(cmd, logger)
		defer wg.Done()

		scanPhpSpyStdout(
			scannerChannel,
			samplesChannel,
			rateHz,
			accumulationInterval,
			entryPoints,
			dynamicTags,
			logger,
		)
	}()

	go func() {
		defer handlePanic(cmd, logger)
		defer wg.Done()

		go sendToPyroscope(
			samplesChannel,
			app,
			staticTags,
			pyroscopeURL,
			pyroscopeAuth,
			rateMb,
			logger,
		)
	}()

	wg.Wait()

	logger.Info("shutting down")

	return nil
}

func main() {
	app := &cli.App{
		Name:  "gospy",
		Usage: "A Go wrapper for phpspy that sends traces to Pyroscope",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "pyroscope",
				Usage:    "Pyroscope server URL",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "pyroscopeAuth",
				Usage: "Pyroscope authentication token",
			},
			&cli.BoolFlag{
				Name:  "debug",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "app",
				Usage: "App Name for pyroscope",
			},
			&cli.StringFlag{
				Name:  "restart",
				Usage: "Restart phpspy if it exited? Allowed values: always, onerror, onsuccess, no. Default: no",
				Value: "no",
			},
			&cli.StringSliceFlag{
				Name:  "tag",
				Usage: "key=value for static tags, key=%value% for dynamic tags",
			},
			&cli.DurationFlag{
				Name:  "accumulation-interval",
				Usage: "Interval between sending accumulated samples to pyroscope",
				Value: DefaultAccumulationPeriod * time.Second,
			},
			&cli.IntFlag{
				Name:  "rate-mb",
				Usage: "Ingestion limit in mb",
				Value: DefaultRateMB,
			},
			&cli.StringSliceFlag{
				Name:  "entrypoint",
				Usage: "Name of entrypoint file to collect data, example: index.php",
			},
		},
		Action: runGoSpy,
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
