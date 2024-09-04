package main

import (
	"hash/fnv"
	"log"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

type Sample struct {
	sample string
	tags   string
	count  int
}

type SampleCollection struct {
	from    time.Time
	to      time.Time
	samples map[uint32]Sample
}

func sampleHash(s string, tags string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	h.Write([]byte(tags))
	return h.Sum32()
}

func newSampleCollection() *SampleCollection {
	return &SampleCollection{
		from:    time.Now(),
		samples: make(map[uint32]Sample),
	}
}

func (sc *SampleCollection) addSample(str string, tags string) {
	hash := sampleHash(str, tags)
	if sample, exists := sc.samples[hash]; !exists {
		sc.samples[hash] = Sample{
			sample: str,
			count:  1,
			tags:   tags,
		}
	} else {
		sample.count++
		sc.samples[hash] = sample
	}
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
			&cli.StringFlag{
				Name:  "app",
				Usage: "App Name for pyroscope",
			},
			&cli.StringSliceFlag{
				Name:  "tag",
				Usage: "key=value for static tags, key=%value% for dynamic tags",
			},
			&cli.DurationFlag{
				Name:  "accumulation-interval",
				Usage: "Interval between sending accumulated samples to Pyroscope",
				Value: 10 * time.Second,
			},
		},
		Action: func(context *cli.Context) error {
			staticTags := make(map[string]string)
			dynamicTags := make(map[string]string)

			pyroscopeURL := context.String("pyroscope")
			pyroscopeAuth := context.String("pyroscopeAuth")
			accumulationInterval := context.Duration("accumulation-interval")
			app := context.String("app")
			args := context.Args().Slice()

			for _, tag := range context.StringSlice("tag") {
				parts := strings.SplitN(tag, "=", 2)
				if len(parts) == 2 {
					key := parts[0]
					value := parts[1]
					lastCharPosition := len(value) - 1
					if value[0:1] == "%" && value[lastCharPosition:] == "%" {
						dynamicTags[value[1:lastCharPosition]] = key
					} else {
						staticTags[key] = value
					}
				}
			}

			samplesChannel := make(chan SampleCollection)

			go func() {
				if err := runPhpspy(samplesChannel, args[1:], dynamicTags, accumulationInterval); err != nil {
					log.Fatalf("Ошибка запуска phpspy: %v", err)
				}
			}()

			sendToPyroscope(samplesChannel, app, staticTags, pyroscopeURL, pyroscopeAuth)

			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
