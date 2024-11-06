package pyroscope

import (
	"context"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	ctx             context.Context
	requestQueue    <-chan *requestData
	pyroscopeClient Client
	statsChannel    chan<- stats
}

func (w *Worker) processRequests() {
	const maxRetries = 2

	for {
		select {
		case <-w.ctx.Done():
			return
		case req, ok := <-w.requestQueue:
			if !ok {
				return
			}
			err := w.handleRequest(req, maxRetries)
			if err != nil {
				// Log the error and continue
				log.Error().Err(err).Msg("error processing request")
			}
		}
	}
}

func NewWorker() *Worker {

}
