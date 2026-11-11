package tests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
)

// TestIdempotentTransfer_SameRequestMultipleTimes tests that sending the same transfer request
// repeatedly returns the original transaction result and transfers gold exactly once.
func TestIdempotentTransfer_SameRequestMultipleTimes(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)   // 1000 mg gold
	receiver := h.CreateTestWallet(t, user2, 500)  // 500 mg gold

	idempotencyKey := "idem-key-unique-001"
	transferAmount := int64(300)

	// First execution
	res1, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver.ID, transferAmount)
	if err != nil {
		t.Fatalf("First transfer failed: %v", err)
	}
	if res1.IsIdempotentReplay {
		t.Errorf("First transfer should not be an idempotent replay")
	}

	// Repeated executions with the EXACT SAME key & payload
	for attempt := 2; attempt <= 5; attempt++ {
		resRetry, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver.ID, transferAmount)
		if err != nil {
			t.Fatalf("Attempt %d failed: %v", attempt, err)
		}
		if !resRetry.IsIdempotentReplay {
			t.Errorf("Attempt %d should have been an idempotent replay", attempt)
		}
		// Must return identical transaction identifier
		if resRetry.Transaction.ID != res1.Transaction.ID {
			t.Errorf("Attempt %d returned transaction ID %s, expected original ID %s",
				attempt, resRetry.Transaction.ID, res1.Transaction.ID)
		}
	}

	// Verify gold balances were mutated EXACTLY ONCE
	senderFinal, _ := h.WalletRepo.GetByID(ctx, sender.ID)
	receiverFinal, _ := h.WalletRepo.GetByID(ctx, receiver.ID)

	expectedSenderBalance := int64(700)   // 1000 - 300
	expectedReceiverBalance := int64(800) // 500 + 300

	if senderFinal.Balance != expectedSenderBalance {
		t.Errorf("Expected sender balance %d, got %d", expectedSenderBalance, senderFinal.Balance)
	}
	if receiverFinal.Balance != expectedReceiverBalance {
		t.Errorf("Expected receiver balance %d, got %d", expectedReceiverBalance, receiverFinal.Balance)
	}

	// Verify exactly two ledger entries created (one credit, one debit for this transfer)
	ledgerEntries, err := h.LedgerRepo.GetEntriesByTransaction(ctx, res1.Transaction.ID)
	if err != nil {
		t.Fatalf("Failed to fetch ledger entries: %v", err)
	}
	if len(ledgerEntries) != 2 {
		t.Errorf("Expected exactly 2 ledger entries for the transaction, found %d", len(ledgerEntries))
	}
}

// TestIdempotentTransfer_ConcurrentDuplicateRequests sends duplicate transfer requests concurrently
// to verify race condition safety. At most one transaction must execute.
func TestIdempotentTransfer_ConcurrentDuplicateRequests(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 200)

	idempotencyKey := "idem-key-concurrent-002"
	transferAmount := int64(250)

	concurrency := 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	type Result struct {
		TxID      uuid.UUID
		Err       error
		IsReplay  bool
	}
	results := make([]Result, concurrency)

	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			res, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver.ID, transferAmount)
			if err != nil {
				results[idx] = Result{Err: err}
			} else {
				results[idx] = Result{TxID: res.Transaction.ID, IsReplay: res.IsIdempotentReplay}
			}
		}()
	}

	wg.Wait()

	// Verify outcome:
	// Every successful goroutine must agree on the exact same Transaction ID
	var canonicalTxID uuid.UUID
	successfulCount := 0

	for _, r := range results {
		if r.Err == nil {
			successfulCount++
			if canonicalTxID == uuid.Nil {
				canonicalTxID = r.TxID
			} else if r.TxID != canonicalTxID {
				t.Errorf("Inconsistent transaction ID returned across concurrent calls: %s != %s", r.TxID, canonicalTxID)
			}
		} else {
			// In concurrent execution, pending calls may receive ErrIdempotencyConflict
			if !errors.Is(r.Err, domain.ErrIdempotencyConflict) {
				t.Logf("Concurrent request returned error: %v", r.Err)
			}
		}
	}

	if canonicalTxID == uuid.Nil {
		t.Fatalf("No request succeeded in concurrent idempotency test")
	}

	// Verify gold was transferred strictly once!
	senderFinal, _ := h.WalletRepo.GetByID(ctx, sender.ID)
	receiverFinal, _ := h.WalletRepo.GetByID(ctx, receiver.ID)

	expectedSenderBal := int64(1000 - 250)
	expectedReceiverBal := int64(200 + 250)

	if senderFinal.Balance != expectedSenderBal {
		t.Errorf("Gold was transferred multiple times! Expected sender balance %d, got %d", expectedSenderBal, senderFinal.Balance)
	}
	if receiverFinal.Balance != expectedReceiverBal {
		t.Errorf("Gold was transferred multiple times! Expected receiver balance %d, got %d", expectedReceiverBal, receiverFinal.Balance)
	}
}

// TestIdempotentTransfer_NetworkTimeoutRetry simulates a network timeout after server commits,
// followed by a client retry.
func TestIdempotentTransfer_NetworkTimeoutRetry(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 800)
	receiver := h.CreateTestWallet(t, user2, 100)

	idempotencyKey := "idem-key-timeout-retry-003"
	amount := int64(300)

	// Step 1: Client submits request, server commits transaction successfully.
	resOriginal, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver.ID, amount)
	if err != nil {
		t.Fatalf("Initial transfer failed: %v", err)
	}

	// Step 2: Client experienced a network timeout on reading HTTP response.
	// Client retries after 500ms with same Idempotency-Key.
	resRetry, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver.ID, amount)
	if err != nil {
		t.Fatalf("Client retry after timeout failed: %v", err)
	}

	// Step 3: Verify client receives original result
	if resRetry.Transaction.ID != resOriginal.Transaction.ID {
		t.Errorf("Retry returned mismatched transaction ID: %s vs %s", resRetry.Transaction.ID, resOriginal.Transaction.ID)
	}
	if !resRetry.IsIdempotentReplay {
		t.Errorf("Expected retry to be flagged as idempotent replay")
	}

	// Verify balance was deducted only once
	senderAfter, _ := h.WalletRepo.GetByID(ctx, sender.ID)
	if senderAfter.Balance != 500 { // 800 - 300
		t.Errorf("Balance deducted multiple times! Expected 500, got %d", senderAfter.Balance)
	}
}

// TestIdempotentTransfer_PayloadMismatchRejection tests that reusing the same idempotency key
// with a different payload is strictly rejected.
func TestIdempotentTransfer_PayloadMismatchRejection(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	user3 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver1 := h.CreateTestWallet(t, user2, 100)
	receiver2 := h.CreateTestWallet(t, user3, 100)

	idempotencyKey := "idem-key-tamper-004"

	// 1. Initial transfer with amount 200 to receiver1
	_, err := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver1.ID, 200)
	if err != nil {
		t.Fatalf("Initial transfer failed: %v", err)
	}

	// 2. Client reuses same key with different amount (400 instead of 200)
	_, errAmountMismatch := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver1.ID, 400)
	if !errors.Is(errAmountMismatch, domain.ErrIdempotencyPayloadMismatch) {
		t.Errorf("Expected ErrIdempotencyPayloadMismatch for altered amount, got: %v", errAmountMismatch)
	}

	// 3. Client reuses same key with different receiver
	_, errReceiverMismatch := h.WalletService.TransferMoney(ctx, idempotencyKey, user1, sender.ID, receiver2.ID, 200)
	if !errors.Is(errReceiverMismatch, domain.ErrIdempotencyPayloadMismatch) {
		t.Errorf("Expected ErrIdempotencyPayloadMismatch for altered receiver, got: %v", errReceiverMismatch)
	}

	// Verify balances: only original 200 was transferred to receiver1
	senderFinal, _ := h.WalletRepo.GetByID(ctx, sender.ID)
	if senderFinal.Balance != 800 {
		t.Errorf("Expected sender balance 800, got %d", senderFinal.Balance)
	}
}

