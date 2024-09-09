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

type Sample struct {
	sample string
	count  int
}

// samples grouped by tags
type SampleCollection struct {
	from    time.Time
	to      time.Time
	samples map[string]map[uint64]*Sample
	rateHz  int
	sync.RWMutex
}

// Function for generating a hash from string and tags
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
			&cli.BoolFlag{
				Name:  "debug",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "pyroscopeAuth",
				Usage: "Pyroscope authentication token",
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
				Value: 10 * time.Second,
			},
		},
		Action: func(context *cli.Context) error {
			samplesChannel := make(chan *SampleCollection, 100) // Buffered channel to avoid blocking

			pyroscopeURL := context.String("pyroscope")
			pyroscopeAuth := context.String("pyroscopeAuth")
			accumulationInterval := context.Duration("accumulation-interval")
			app := context.String("app")
			arguments := context.Args().Slice()
			debug := context.Bool("debug")
			restart := context.String("restart")
			staticTags, dynamicTags, tagsErr := getTags(context.StringSlice("tag"))

			if tagsErr != nil {
				return tagsErr
			}

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

			logger.Info("gospy started")
			logger.Debug("gospy params",
				zap.String("pyroscope url", pyroscopeURL),
				zap.String("pyroscope auth token", pyroscopeAuth),
				zap.Duration("phpspy accumulation-interval", accumulationInterval),
			)

			go func() {
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
						break
					}
				}
			}()

			sendToPyroscope(samplesChannel, app, staticTags, pyroscopeURL, pyroscopeAuth, logger)

			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
