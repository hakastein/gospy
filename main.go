package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

const (
	DefaultRateMB             = 4
	DefaultAccumulationPeriod = 10
	Megabyte                  = 1048576
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

func runGoSpy(context *cli.Context) error {
	pyroscopeURL := context.String("pyroscope")
	pyroscopeAuth := context.String("pyroscopeAuth")
	accumulationInterval := context.Duration("accumulation-interval")
	app := context.String("app")
	arguments := context.Args().Slice()
	debug := context.Bool("debug")
	restart := context.String("restart")
	rateMb := context.Int("rate-mb") * Megabyte
	staticTags, dynamicTags, tagsErr := getTags(context.StringSlice("tag"))

	if tagsErr != nil {
		return tagsErr
	}

	samplesChannel := make(chan *SampleCollection) // Buffered channel until avoid blocking

	var logger *zap.Logger
	var err error
	if debug {
		cfg := zap.NewDevelopmentConfig()
		logger, err = cfg.Build()
	} else {
		cfg := zap.NewProductionConfig()
		logger, err = cfg.Build()
	}

	if err != nil {
		log.Fatalf("logger subsystem error: %s", err)
	}
	defer logger.Sync()

	logger.Info("gospy started",
		zap.String("pyroscope url", pyroscopeURL),
		zap.String("pyroscope auth token", pyroscopeAuth),
		zap.String("static tags", staticTags),
		zap.Duration("phpspy accumulation-interval", accumulationInterval),
	)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer close(samplesChannel)

		for {
			err := runPhpspy(samplesChannel, arguments[1:], dynamicTags, accumulationInterval, logger)
			if err != nil {
				logger.Error("phpspy exited with error", zap.Error(err))
			}

			switch {
			case restart == "always":
				continue
			case restart == "onerror" && err != nil:
				continue
			case restart == "onsuccess" && err == nil:
				continue
			default:
				return
			}
		}

	}()

	go sendToPyroscope(
		samplesChannel,
		app,
		staticTags,
		pyroscopeURL,
		pyroscopeAuth,
		rateMb,
		logger,
	)

	wg.Wait()
	return nil
}

func main() {
	app := &cli.App{
		Name:  "gospy",
		Usage: "A Go wrapper for phpspy that sends traces until Pyroscope",
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
				Usage: "Interval between sending accumulated samples until pyroscope",
				Value: DefaultAccumulationPeriod * time.Second,
			},
			&cli.IntFlag{
				Name:  "rate-mb",
				Usage: "Ingestion limit in mb",
				Value: DefaultRateMB,
			},
		},
		Action: runGoSpy,
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
