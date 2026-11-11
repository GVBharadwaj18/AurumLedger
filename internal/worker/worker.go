package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"aurumledger/internal/domain"
	"aurumledger/pkg/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrPoisonMessage = errors.New("poison message: malformed or unprocessable payload")
	ErrRetryable     = errors.New("transient failure: retryable error")
)

// MessageDelivery abstracts RabbitMQ amqp.Delivery for testing and production.
type MessageDelivery interface {
	GetBody() []byte
	GetMessageID() string
	Ack() error
	Nack(requeue bool) error
}

type AMQPDelivery struct {
	delivery amqp.Delivery
}

func (a *AMQPDelivery) GetBody() []byte       { return a.delivery.Body }
func (a *AMQPDelivery) GetMessageID() string  { return a.delivery.MessageId }
func (a *AMQPDelivery) Ack() error            { return a.delivery.Ack(false) }
func (a *AMQPDelivery) Nack(requeue bool) error { return a.delivery.Nack(false, requeue) }

type Worker struct {
	mq             *rabbitmq.RabbitMQ
	processedRepo  domain.ProcessedEventRepository
	consumerName   string
	maxRetries     int
	baseBackoff    time.Duration
	sideEffectHook func(event domain.TransferEvent) error // For testing or audit notifications
	wg             sync.WaitGroup
}

func NewWorker(
	mq *rabbitmq.RabbitMQ,
	pRepo domain.ProcessedEventRepository,
) *Worker {
	return &Worker{
		mq:            mq,
		processedRepo: pRepo,
		consumerName:  "aurumledger.transfer.worker",
		maxRetries:    3,
		baseBackoff:   100 * time.Millisecond,
	}
}

// SetSideEffectHook allows injecting custom side-effects or simulating errors in tests.
func (w *Worker) SetSideEffectHook(hook func(event domain.TransferEvent) error) {
	w.sideEffectHook = hook
}

// ProcessMessage processes a single message delivery following Priority 4 requirements:
// 1. Decodes payload. If poison -> Nack(false) to DLQ immediately.
// 2. Checks processed_events table for idempotency. If duplicate -> Ack() immediately without side effects.
// 3. Executes side effects with retry & backoff.
// 4. Persists processed_events record.
// 5. Acknowledges message ONLY after successful processing.
func (w *Worker) ProcessMessage(ctx context.Context, msg MessageDelivery) error {
	body := msg.GetBody()

	// Step 1: Decode JSON payload
	var event domain.TransferEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("[Worker] Poison message detected (%v). Routing directly to Dead-Letter Queue (DLQ)", err)
		// Nack with requeue=false routes message to x-dead-letter-exchange (DLX)
		_ = msg.Nack(false)
		return fmt.Errorf("%w: %v", ErrPoisonMessage, err)
	}

	// Validate essential event fields
	if event.TransactionID.String() == "00000000-0000-0000-0000-000000000000" || event.Amount <= 0 {
		log.Printf("[Worker] Invalid event fields in message %s. Routing to DLQ", event.EventID)
		_ = msg.Nack(false)
		return fmt.Errorf("%w: invalid event content", ErrPoisonMessage)
	}

	// Step 2: Idempotent Consumer check via PostgreSQL
	alreadyProcessed, err := w.processedRepo.IsProcessed(ctx, event.EventID)
	if err != nil {
		log.Printf("[Worker] Failed to check processed status for event %s: %v. Will retry.", event.EventID, err)
		_ = msg.Nack(true) // Requeue on transient DB error
		return err
	}

	if alreadyProcessed {
		log.Printf("[Worker] Idempotency: Event %s already processed. Acknowledging duplicate safely.", event.EventID)
		return msg.Ack()
	}

	// Step 3: Execute Business Side Effects with Retry & Exponential Backoff
	var lastErr error
	for attempt := 0; attempt <= w.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(w.baseBackoff) * math.Pow(2, float64(attempt-1)))
			log.Printf("[Worker] Retrying event %s (attempt %d/%d) after %v...", event.EventID, attempt, w.maxRetries, backoff)
			select {
			case <-ctx.Done():
				_ = msg.Nack(true) // Requeue on shutdown
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		if w.sideEffectHook != nil {
			lastErr = w.sideEffectHook(event)
		} else {
			lastErr = w.defaultSideEffect(event)
		}

		if lastErr == nil {
			break
		}
	}

	// If all retries failed
	if lastErr != nil {
		log.Printf("[Worker] All %d retries exhausted for event %s: %v. Sending to DLQ.", w.maxRetries, event.EventID, lastErr)
		_ = msg.Nack(false) // Send to DLQ
		return lastErr
	}

	// Step 4: Record processed event in PostgreSQL
	if err := w.processedRepo.MarkProcessed(ctx, nil, event.EventID, "TRANSFER_NOTIFICATION", w.consumerName); err != nil {
		log.Printf("[Worker] Failed to mark event %s as processed in DB: %v. Requeueing.", event.EventID, err)
		_ = msg.Nack(true)
		return err
	}

	// Step 5: Acknowledge message ONLY after successful processing and DB write
	if err := msg.Ack(); err != nil {
		log.Printf("[Worker] Failed to Ack message %s: %v", event.EventID, err)
		return err
	}

	log.Printf("[Worker] Successfully processed and acknowledged event %s for transaction %s", event.EventID, event.TransactionID)
	return nil
}

func (w *Worker) defaultSideEffect(event domain.TransferEvent) error {
	log.Printf("[AuditWorker] Transfer confirmed: %d mg gold from wallet %s to %s (Tx: %s)",
		event.Amount, event.SenderID, event.ReceiverID, event.TransactionID)
	return nil
}

// Start launches the consumer loop with graceful shutdown support.
func (w *Worker) Start(ctx context.Context) error {
	if w.mq == nil {
		log.Println("[Worker] RabbitMQ connection not configured, skipping worker start")
		return nil
	}

	deliveries, err := w.mq.Consume()
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		log.Println("[Worker] RabbitMQ worker started consuming from durable queue")

		for {
			select {
			case <-ctx.Done():
				log.Println("[Worker] Context cancelled, stopping worker consumer")
				return
			case d, ok := <-deliveries:
				if !ok {
					log.Println("[Worker] Deliveries channel closed")
					return
				}
				delivery := &AMQPDelivery{delivery: d}
				_ = w.ProcessMessage(ctx, delivery)
			}
		}
	}()

	return nil
}

// Stop waits for in-flight messages to complete processing.
func (w *Worker) Stop() {
	w.wg.Wait()
	log.Println("[Worker] Worker shutdown cleanly")
}
