package main

import (
	"os"

	"github.com/rs/zerolog/log"

	"github.com/hakastein/gospy/internal/app"
	"github.com/hakastein/gospy/internal/cli"
)

func main() {
	if err := cli.New(app.Run).Run(os.Args); err != nil {
		log.Fatal().Err(err).Msg("can't start app")
	}
}
