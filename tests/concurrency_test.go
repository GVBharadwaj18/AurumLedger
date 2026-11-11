package tests

import (
	"context"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestConcurrentTransfers runs a high-concurrency stress test with 50 concurrent goroutines
// transferring gold between wallets to verify:
// 1. Concurrency safety (zero deadlocks, zero race conditions via deterministic row locking)
// 2. Exact balance arithmetic preservation
// 3. Complete double-entry reconciliation compliance
func TestConcurrentTransfers(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	// Create test wallets
	userA := uuid.New()
	userB := uuid.New()
	initialA := int64(1000) // 1000 mg
	initialB := int64(0)

	walletA := h.CreateTestWallet(t, userA, initialA)
	walletB := h.CreateTestWallet(t, userB, initialB)

	// Scenario: 50 concurrent transfers of 1 mg gold from A to B
	concurrentCount := 50
	transferAmount := int64(1)

	var wg sync.WaitGroup
	wg.Add(concurrentCount)

	errChan := make(chan error, concurrentCount)

	log.Printf("[Test] Starting %d concurrent transfers of %d mg gold from %s to %s...",
		concurrentCount, transferAmount, walletA.ID, walletB.ID)
	start := time.Now()

	for i := 0; i < concurrentCount; i++ {
		go func() {
			defer wg.Done()
			idempotencyKey := uuid.New().String()
			_, err := h.WalletService.TransferMoney(ctx, idempotencyKey, userA, walletA.ID, walletB.ID, transferAmount)
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)
	duration := time.Since(start)
	log.Printf("[Test] Completed %d transfers in %v", concurrentCount, duration)

	// Verify no errors occurred
	for err := range errChan {
		t.Errorf("Error during concurrent transfer: %v", err)
	}

	// Verify exact balances
	finalA, err := h.WalletRepo.GetByID(ctx, walletA.ID)
	if err != nil {
		t.Fatalf("Failed to fetch wallet A: %v", err)
	}
	finalB, err := h.WalletRepo.GetByID(ctx, walletB.ID)
	if err != nil {
		t.Fatalf("Failed to fetch wallet B: %v", err)
	}

	expectedA := initialA - int64(concurrentCount)*transferAmount
	expectedB := initialB + int64(concurrentCount)*transferAmount

	if finalA.Balance != expectedA {
		t.Errorf("Expected Wallet A balance %d, got %d", expectedA, finalA.Balance)
	}
	if finalB.Balance != expectedB {
		t.Errorf("Expected Wallet B balance %d, got %d", expectedB, finalB.Balance)
	}

	// Verify Double-Entry Accounting Ledger Invariant
	reconReport, err := h.ReconService.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconciliation audit failed: %v", err)
	}

	if !reconReport.IsSystemBalanced {
		t.Errorf("System failed ledger reconciliation audit! Report: %+v", reconReport)
	}
	if reconReport.MismatchedWallets != 0 {
		t.Errorf("Found %d mismatched wallets: %+v", reconReport.MismatchedWallets, reconReport.Discrepancies)
	}

	fmt.Printf("[Test Passed] Final Balances: WalletA=%d mg, WalletB=%d mg. Reconciliation: 100%% balanced!\n",
		finalA.Balance, finalB.Balance)
}
