package pyroscope

import (
	"context"

	"golang.org/x/time/rate"

	"github.com/hakastein/gospy/internal/collector"
)

// Processor handles the business logic of processing profile data
type Processor struct {
	client      *Client
	appMetadata *AppMetadata
	rateLimiter *rate.Limiter
}

// NewProcessor creates a new Processor instance
func NewProcessor(client *Client, appMetadata *AppMetadata, rateLimiter *rate.Limiter) *Processor {
	return &Processor{
		client:      client,
		appMetadata: appMetadata,
		rateLimiter: rateLimiter,
	}
}

// ProcessData processes a single TagCollection, respecting rate limits and sending to Pyroscope
func (p *Processor) ProcessData(ctx context.Context, profileData *collector.TagCollection) error {
	dataSize := profileData.Len()

	// Respect rate limiting
	if err := p.rateLimiter.WaitN(ctx, dataSize); err != nil {
		return err
	}

	payload := p.appMetadata.NewPayload(profileData)
	return p.client.Send(ctx, payload)
}
