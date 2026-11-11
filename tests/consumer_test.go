package tests

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
)

// TestConsumer_IdempotentDuplicateDelivery verifies that when RabbitMQ redelivers
// a message (at-least-once delivery), the consumer detects that the event was
// already processed, ACKs the message safely, and does NOT execute duplicate side effects.
func TestConsumer_IdempotentDuplicateDelivery(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	eventID := uuid.New()
	txID := uuid.New()
	senderID := uuid.New()
	receiverID := uuid.New()

	event := domain.TransferEvent{
		EventID:       eventID,
		TransactionID: txID,
		SenderID:      senderID,
		ReceiverID:    receiverID,
		Amount:        500,
		Timestamp:     time.Now().UTC(),
	}
	body, _ := json.Marshal(event)

	// Side-effect counter
	sideEffectExecutions := 0
	h.Worker.SetSideEffectHook(func(e domain.TransferEvent) error {
		sideEffectExecutions++
		return nil
	})

	// 1. First Delivery
	delivery1 := NewMockDelivery(body, eventID.String())
	err := h.Worker.ProcessMessage(ctx, delivery1)
	if err != nil {
		t.Fatalf("First message processing failed: %v", err)
	}
	if !delivery1.AckCalled {
		t.Errorf("First message was not acknowledged")
	}
	if sideEffectExecutions != 1 {
		t.Errorf("Expected exactly 1 side effect execution, got %d", sideEffectExecutions)
	}

	// Verify event was marked as processed in DB
	isProcessed, err := h.ProcessedRepo.IsProcessed(ctx, eventID)
	if err != nil || !isProcessed {
		t.Fatalf("Event was not recorded in processed_events: %v", err)
	}

	// 2. Redelivered Message (Duplicate Delivery)
	delivery2 := NewMockDelivery(body, eventID.String())
	err = h.Worker.ProcessMessage(ctx, delivery2)
	if err != nil {
		t.Fatalf("Redelivered message processing failed: %v", err)
	}
	if !delivery2.AckCalled {
		t.Errorf("Duplicate message was not acknowledged")
	}

	// Side effect must NOT be executed a second time!
	if sideEffectExecutions != 1 {
		t.Errorf("Duplicate delivery caused repeated side effects! Count: %d", sideEffectExecutions)
	}
}

// TestConsumer_PoisonMessageRoutedToDLQ verifies that a malformed or invalid payload
// is rejected (Nack with requeue=false) so it routes to the Dead-Letter Queue (DLQ),
// and the worker continues operating without crashing.
func TestConsumer_PoisonMessageRoutedToDLQ(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	// Malformed JSON (Poison message)
	poisonBody := []byte("{ invalid-json-payload-corrupted-bytes }")
	delivery := NewMockDelivery(poisonBody, "poison-msg-001")

	err := h.Worker.ProcessMessage(ctx, delivery)
	if err == nil {
		t.Errorf("Expected error for poison message, got nil")
	}

	// Message MUST be Nack'd with requeue=false (routed to DLQ)
	if !delivery.NackCalled {
		t.Errorf("Poison message was not NACKed")
	}
	if delivery.Requeued {
		t.Errorf("Poison message should NOT be requeued (requeue must be false for DLQ routing)")
	}
	if delivery.AckCalled {
		t.Errorf("Poison message should never be ACKed")
	}
}

// TestConsumer_RetryAndExponentialBackoff verifies that temporary processing failures
// trigger retry with backoff, and when retries are exhausted, the message is routed to DLQ.
func TestConsumer_RetryAndExponentialBackoff(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	event := domain.TransferEvent{
		EventID:       uuid.New(),
		TransactionID: uuid.New(),
		SenderID:      uuid.New(),
		ReceiverID:    uuid.New(),
		Amount:        100,
		Timestamp:     time.Now().UTC(),
	}
	body, _ := json.Marshal(event)

	attempts := 0
	h.Worker.SetSideEffectHook(func(e domain.TransferEvent) error {
		attempts++
		return errors.New("simulated transient network failure to external service")
	})

	delivery := NewMockDelivery(body, event.EventID.String())
	err := h.Worker.ProcessMessage(ctx, delivery)
	if err == nil {
		t.Errorf("Expected error after retries exhausted, got nil")
	}

	// Verify retried 3 times (1 initial + 3 retries = 4 total attempts)
	if attempts != 4 {
		t.Errorf("Expected 4 total attempts with retries, got %d", attempts)
	}

	// Verify routed to DLQ after exhaustion
	if !delivery.NackCalled || delivery.Requeued {
		t.Errorf("Exhausted message should be NACKed with requeue=false for DLQ routing")
	}
	if delivery.AckCalled {
		t.Errorf("Failed message should not be ACKed")
	}

	// Verify not marked as processed
	isProcessed, _ := h.ProcessedRepo.IsProcessed(ctx, event.EventID)
	if isProcessed {
		t.Errorf("Failed event should not be marked as processed")
	}
}

// TestConsumer_TransientFailureRecovery verifies that if a transient failure resolves
// on retry, the message succeeds and is ACKed.
func TestConsumer_TransientFailureRecovery(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	event := domain.TransferEvent{
		EventID:       uuid.New(),
		TransactionID: uuid.New(),
		SenderID:      uuid.New(),
		ReceiverID:    uuid.New(),
		Amount:        200,
		Timestamp:     time.Now().UTC(),
	}
	body, _ := json.Marshal(event)

	attempts := 0
	h.Worker.SetSideEffectHook(func(e domain.TransferEvent) error {
		attempts++
		if attempts < 2 {
			return errors.New("temporary database hiccup")
		}
		return nil // Success on attempt 2!
	})

	delivery := NewMockDelivery(body, event.EventID.String())
	err := h.Worker.ProcessMessage(ctx, delivery)
	if err != nil {
		t.Fatalf("Expected message to succeed after retry, got: %v", err)
	}

	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
	if !delivery.AckCalled {
		t.Errorf("Recovered message should be ACKed")
	}
	if delivery.NackCalled {
		t.Errorf("Recovered message should not be NACKed")
	}

	// Verify marked as processed
	isProcessed, _ := h.ProcessedRepo.IsProcessed(ctx, event.EventID)
	if !isProcessed {
		t.Errorf("Recovered message should be recorded in processed_events")
	}
}

