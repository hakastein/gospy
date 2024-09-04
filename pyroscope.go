package main

import (
	"fmt"
	"log"
)

func sendToPyroscope(channel chan SampleCollection, pyroscopUrl string, pyroscopAuth string) {
	log.Print("send to pyroscope")
	for {
		val, ok := <-channel
		if ok {
			fmt.Print("------------------\n")
			for _, sample := range val.samples {
				fmt.Printf("%#v\n", sample)
			}
		} else {
			break // exit break loop
		}
	}
}
