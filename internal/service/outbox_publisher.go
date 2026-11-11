package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
)

// MessageBrokerPublisher defines the interface required by the OutboxPublisher.
type MessageBrokerPublisher interface {
	Publish(ctx context.Context, messageID string, body []byte) error
}

type OutboxPublisher struct {
	outboxRepo   domain.OutboxRepository
	broker       MessageBrokerPublisher
	pollInterval time.Duration
	batchSize    int
}

func NewOutboxPublisher(
	outboxRepo domain.OutboxRepository,
	broker MessageBrokerPublisher,
	pollInterval time.Duration,
	batchSize int,
) *OutboxPublisher {
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	return &OutboxPublisher{
		outboxRepo:   outboxRepo,
		broker:       broker,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

// ProcessBatch fetches pending outbox events (using SELECT FOR UPDATE SKIP LOCKED)
// and publishes them to the message broker.
func (p *OutboxPublisher) ProcessBatch(ctx context.Context) (int, error) {
	events, err := p.outboxRepo.FetchPending(ctx, nil, p.batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}

	publishedCount := 0
	for _, event := range events {
		if p.broker == nil {
			// Broker not configured or temporarily unavailable
			continue
		}

		// Attempt to publish to broker with persistent delivery mode
		err := p.broker.Publish(ctx, event.ID.String(), []byte(event.Payload))
		if err != nil {
			log.Printf("[OutboxPublisher] Failed to publish event %s (retry %d): %v", event.ID, event.RetryCount+1, err)
			_ = p.outboxRepo.MarkFailed(ctx, event.ID, err.Error(), event.RetryCount+1)
			continue
		}

		// Mark as published in outbox
		if err := p.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			log.Printf("[OutboxPublisher] Failed to mark event %s as published: %v", event.ID, err)
			continue
		}

		publishedCount++
	}

	return publishedCount, nil
}

// Start launches the outbox polling loop until context cancellation.
func (p *OutboxPublisher) Start(ctx context.Context) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	log.Println("[OutboxPublisher] Started background outbox publisher loop")

	for {
		select {
		case <-ctx.Done():
			log.Println("[OutboxPublisher] Stopping outbox publisher gracefully")
			return
		case <-ticker.C:
			_, err := p.ProcessBatch(ctx)
			if err != nil {
				log.Printf("[OutboxPublisher] Error processing batch: %v", err)
			}
		}
	}
}

// SetBroker allows dynamic broker injection (useful for failover tests).
func (p *OutboxPublisher) SetBroker(broker MessageBrokerPublisher) {
	p.broker = broker
}

// GetOutboxRepo returns the underlying outbox repo.
func (p *OutboxPublisher) GetOutboxRepo() domain.OutboxRepository {
	return p.outboxRepo
}

// PublishManual exposes manual publishing for testing specific events.
func (p *OutboxPublisher) PublishManual(ctx context.Context, id uuid.UUID) error {
	return p.outboxRepo.MarkPublished(ctx, id)
}

