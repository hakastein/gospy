package types

import (
	"bufio"
	"context"
	"time"
)

type Parser interface {
	Parse(
		ctx context.Context,
		scanner *bufio.Scanner,
		samplesChannel chan<- *Sample,
	)
}

type Sample struct {
	Time  time.Time
	Trace string
	Tags  string
}
