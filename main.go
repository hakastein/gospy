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
	tags   map[string]string
	count  int
}

func sampleHash(s string, tags map[string]string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	for k, v := range tags {
		h.Write([]byte(k))
		h.Write([]byte(v))
	}
	return h.Sum32()
}

type SampleCollection struct {
	from    time.Time
	to      time.Time
	samples map[uint32]Sample
}

func newSampleCollection() *SampleCollection {
	return &SampleCollection{
		from:    time.Now(),
		samples: make(map[uint32]Sample),
	}
}

func (sc *SampleCollection) addSample(str string, tags map[string]string) {
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
			&cli.StringSliceFlag{
				Name:  "tag-from",
				Usage: "Map tags from trace to custom tag names",
			},
			&cli.DurationFlag{
				Name:  "accumulation-interval",
				Usage: "Interval between sending accumulated samples to Pyroscope",
				Value: 2 * time.Second,
			},
		},
		Action: func(context *cli.Context) error {
			pyroscopeURL := context.String("pyroscope")
			pyroscopeAuth := context.String("pyroscopeAuth")
			tags := make(map[string]string)
			for _, tag := range context.StringSlice("tag-from") {
				parts := strings.SplitN(tag, "-", 2)
				if len(parts) == 2 {
					tags[parts[0]] = parts[1]
				}
			}
			accumulationInterval := context.Duration("accumulation-interval")
			args := context.Args().Slice()

			samplesChannel := make(chan SampleCollection)

			go func() {
				if err := runPhpspy(samplesChannel, args[1:], tags, accumulationInterval); err != nil {
					log.Fatalf("Ошибка запуска phpspy: %v", err)
				}
			}()

			sendToPyroscope(samplesChannel, pyroscopeURL, pyroscopeAuth)

			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
