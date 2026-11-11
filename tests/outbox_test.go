package tests

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"aurumledger/internal/domain"
	"aurumledger/internal/service"
	"github.com/google/uuid"
)

// TestOutbox_AtomicWithTransfer verifies that every committed transfer atomically
// creates its corresponding outbox event in PostgreSQL.
func TestOutbox_AtomicWithTransfer(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 500)

	res, err := h.WalletService.TransferMoney(ctx, "outbox-atomic-001", user1, sender.ID, receiver.ID, 250)
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// Query outbox_events table directly for this transaction
	var event domain.OutboxEvent
	if err := h.DB.First(&event, "aggregate_id = ?", res.Transaction.ID).Error; err != nil {
		t.Fatalf("Failed to find outbox event in DB: %v", err)
	}

	if event.AggregateID != res.Transaction.ID {
		t.Errorf("Expected AggregateID %s, got %s", res.Transaction.ID, event.AggregateID)
	}
	if event.Status != "PENDING" {
		t.Errorf("Expected initial outbox status PENDING, got %s", event.Status)
	}

	// Verify payload contains transfer event details
	var payload domain.TransferEvent
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		t.Fatalf("Failed to unmarshal outbox event payload: %v", err)
	}
	if payload.TransactionID != res.Transaction.ID {
		t.Errorf("Payload TransactionID mismatch: %s vs %s", payload.TransactionID, res.Transaction.ID)
	}
	if payload.Amount != 250 {
		t.Errorf("Payload Amount mismatch: expected 250, got %d", payload.Amount)
	}
}

// TestOutbox_RabbitMQUnavailableRetries simulates RabbitMQ broker outage when transfer commits.
// Transfer MUST succeed, and outbox event remains in PENDING state with incremented retry count.
func TestOutbox_RabbitMQUnavailableRetries(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	// 1. Simulate RabbitMQ downtime
	h.Broker.SetAvailable(false)

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 500)

	// 2. Transfer must still succeed! The API does not wait on RabbitMQ.
	res, err := h.WalletService.TransferMoney(ctx, "outbox-broker-down-002", user1, sender.ID, receiver.ID, 300)
	if err != nil {
		t.Fatalf("Transfer should succeed even if broker is down: %v", err)
	}

	// 3. Outbox Publisher attempts to process pending events
	published, err := h.OutboxPublisher.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if published != 0 {
		t.Errorf("Expected 0 published messages while broker is down, got %d", published)
	}

	// 4. Verify outbox event remains in DB as PENDING with retry count = 1
	var savedEvent domain.OutboxEvent
	if err := h.DB.First(&savedEvent, "aggregate_id = ?", res.Transaction.ID).Error; err != nil {
		t.Fatalf("Failed to query outbox event: %v", err)
	}
	if savedEvent.Status != "PENDING" {
		t.Errorf("Expected status PENDING, got %s", savedEvent.Status)
	}
	if savedEvent.RetryCount != 1 {
		t.Errorf("Expected RetryCount 1, got %d", savedEvent.RetryCount)
	}
	if savedEvent.LastError == nil {
		t.Errorf("Expected LastError to be populated with connection error")
	}

	// 5. Simulate RabbitMQ coming back online!
	h.Broker.SetAvailable(true)

	// 6. Next outbox publisher pass succeeds
	publishedRecovered, err := h.OutboxPublisher.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch after broker recovery failed: %v", err)
	}
	if publishedRecovered != 1 {
		t.Errorf("Expected 1 message published after recovery, got %d", publishedRecovered)
	}

	// 7. Verify outbox event is now marked PUBLISHED
	remainingPending, _ := h.OutboxRepo.FetchPending(ctx, nil, 10)
	if len(remainingPending) != 0 {
		t.Errorf("Expected 0 remaining pending events, got %d", len(remainingPending))
	}

	if h.Broker.GetPublishedCount() != 1 {
		t.Errorf("Expected broker to have received 1 published message, got %d", h.Broker.GetPublishedCount())
	}
	_ = res
}

// TestOutbox_ApplicationRestartRecoversPendingEvents verifies that if the application
// crashes or restarts, pending events in PostgreSQL are picked up and published.
func TestOutbox_ApplicationRestartRecoversPendingEvents(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 500)

	// Transfer created while broker was unavailable
	h.Broker.SetAvailable(false)
	_, err := h.WalletService.TransferMoney(ctx, "restart-tx-001", user1, sender.ID, receiver.ID, 150)
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// Simulate Application Crash / Restart:
	// New OutboxPublisher instance connects to the same DB
	h.Broker.SetAvailable(true) // Broker is online when new app instance boots
	newPublisher := service.NewOutboxPublisher(h.OutboxRepo, h.Broker, 50*time.Millisecond, 50)

	// Publisher runs its recovery batch on boot
	published, err := newPublisher.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("Restart recovery batch failed: %v", err)
	}

	if published != 1 {
		t.Errorf("Expected 1 event published during boot recovery, got %d", published)
	}
	if h.Broker.GetPublishedCount() != 1 {
		t.Errorf("Expected message in broker after restart, got %d", h.Broker.GetPublishedCount())
	}
}

// TestOutbox_MultiplePublishersConcurrencySafe simulates multiple publisher instances
// (e.g. horizontally scaled pods) processing pending events.
func TestOutbox_MultiplePublishersConcurrencySafe(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 5000)
	receiver := h.CreateTestWallet(t, user2, 100)

	// Create 10 transfers
	h.Broker.SetAvailable(false) // hold publishing
	for i := 0; i < 10; i++ {
		key := uuid.New().String()
		_, err := h.WalletService.TransferMoney(ctx, key, user1, sender.ID, receiver.ID, 10)
		if err != nil {
			t.Fatalf("Transfer %d failed: %v", i, err)
		}
	}
	h.Broker.SetAvailable(true)

	// Spawn 3 concurrent publisher instances
	publisherCount := 3
	var wg sync.WaitGroup
	wg.Add(publisherCount)

	totalPublished := 0
	var mu sync.Mutex

	for p := 0; p < publisherCount; p++ {
		pub := service.NewOutboxPublisher(h.OutboxRepo, h.Broker, 10*time.Millisecond, 10)
		go func() {
			defer wg.Done()
			count, _ := pub.ProcessBatch(ctx)
			mu.Lock()
			totalPublished += count
			mu.Unlock()
		}()
	}

	wg.Wait()

	// All 10 events must be published without duplicates
	pendingAfter, _ := h.OutboxRepo.FetchPending(ctx, nil, 50)
	if len(pendingAfter) != 0 {
		// Run another round if batching split the work
		extra, _ := h.OutboxPublisher.ProcessBatch(ctx)
		totalPublished += extra
	}

	if totalPublished != 10 {
		t.Errorf("Expected 10 total published events across all publisher instances, got %d", totalPublished)
	}
}
