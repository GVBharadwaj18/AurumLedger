package tests

import (
	"context"
	"errors"
	"sync"
	"testing"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
)

// TestDoubleEntryLedger_BalancedEntries verifies that every completed transfer generates
// balanced debit and credit ledger entries referencing the same transaction identifier.
func TestDoubleEntryLedger_BalancedEntries(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 500)

	transferAmount := int64(350)
	res, err := h.WalletService.TransferMoney(ctx, "ledger-tx-001", user1, sender.ID, receiver.ID, transferAmount)
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// Fetch ledger entries for this transaction
	entries, err := h.LedgerRepo.GetEntriesByTransaction(ctx, res.Transaction.ID)
	if err != nil {
		t.Fatalf("Failed to fetch ledger entries: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("Expected exactly 2 balanced ledger entries, got %d", len(entries))
	}

	var senderEntry, receiverEntry *domain.LedgerEntry
	for i := range entries {
		if entries[i].WalletID == sender.ID {
			senderEntry = &entries[i]
		} else if entries[i].WalletID == receiver.ID {
			receiverEntry = &entries[i]
		}
	}

	if senderEntry == nil || receiverEntry == nil {
		t.Fatalf("Missing sender or receiver ledger entry in transaction %s", res.Transaction.ID)
	}

	// Verify Double-Entry accounting invariants:
	// Sender must have CREDIT entry (gold asset decrease)
	if senderEntry.EntryType != "CREDIT" {
		t.Errorf("Expected sender EntryType CREDIT, got %s", senderEntry.EntryType)
	}
	if senderEntry.Amount != transferAmount {
		t.Errorf("Expected sender entry amount %d, got %d", transferAmount, senderEntry.Amount)
	}
	if senderEntry.BalanceAfter != (1000 - transferAmount) {
		t.Errorf("Expected sender BalanceAfter %d, got %d", 1000-transferAmount, senderEntry.BalanceAfter)
	}

	// Receiver must have DEBIT entry (gold asset increase)
	if receiverEntry.EntryType != "DEBIT" {
		t.Errorf("Expected receiver EntryType DEBIT, got %s", receiverEntry.EntryType)
	}
	if receiverEntry.Amount != transferAmount {
		t.Errorf("Expected receiver entry amount %d, got %d", transferAmount, receiverEntry.Amount)
	}
	if receiverEntry.BalanceAfter != (500 + transferAmount) {
		t.Errorf("Expected receiver BalanceAfter %d, got %d", 500+transferAmount, receiverEntry.BalanceAfter)
	}

	// Double-entry balancing invariant: Sum(Debits) == Sum(Credits)
	if senderEntry.Amount != receiverEntry.Amount {
		t.Errorf("Ledger is out of balance! Debit amount (%d) != Credit amount (%d)",
			receiverEntry.Amount, senderEntry.Amount)
	}
}

// TestDoubleEntryLedger_FailedTransferNoPartialRecords verifies that a failed transfer
// (e.g., insufficient gold balance) rolls back completely and writes ZERO partial ledger entries.
func TestDoubleEntryLedger_FailedTransferNoPartialRecords(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 100) // only 100 mg gold
	receiver := h.CreateTestWallet(t, user2, 50)

	// Attempt to transfer 500 mg (more than available balance)
	_, err := h.WalletService.TransferMoney(ctx, "fail-tx-002", user1, sender.ID, receiver.ID, 500)
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("Expected ErrInsufficientFunds, got: %v", err)
	}

	// Verify neither sender nor receiver has any new ledger entries beyond initial deposits
	senderEntries, err := h.LedgerRepo.GetEntriesByWallet(ctx, sender.ID)
	if err != nil {
		t.Fatalf("Failed to fetch sender ledger: %v", err)
	}
	// Only initial deposit entry (1 entry) should exist
	if len(senderEntries) != 1 {
		t.Errorf("Failed transfer created partial ledger entries! Found %d entries for sender", len(senderEntries))
	}

	// Verify wallet balances remained untouched
	senderAfter, _ := h.WalletRepo.GetByID(ctx, sender.ID)
	receiverAfter, _ := h.WalletRepo.GetByID(ctx, receiver.ID)

	if senderAfter.Balance != 100 {
		t.Errorf("Sender balance mutated on failed transfer: expected 100, got %d", senderAfter.Balance)
	}
	if receiverAfter.Balance != 50 {
		t.Errorf("Receiver balance mutated on failed transfer: expected 50, got %d", receiverAfter.Balance)
	}
}

// TestDoubleEntryLedger_ConcurrentTransfersPreserveInvariants tests that running multiple
// concurrent transfers preserves the total system gold quantity invariant.
func TestDoubleEntryLedger_ConcurrentTransfersPreserveInvariants(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	walletA := h.CreateTestWallet(t, userA, 1000)
	walletB := h.CreateTestWallet(t, userB, 1000)

	initialSystemBalance := walletA.Balance + walletB.Balance // 2000 mg

	concurrency := 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			key := uuid.New().String()
			if idx%2 == 0 {
				// A -> B: 10 mg
				_, _ = h.WalletService.TransferMoney(ctx, key, userA, walletA.ID, walletB.ID, 10)
			} else {
				// B -> A: 10 mg
				_, _ = h.WalletService.TransferMoney(ctx, key, userB, walletB.ID, walletA.ID, 10)
			}
		}()
	}

	wg.Wait()

	// Invariant 1: Total wallet balances must exactly equal initial system balance
	finalA, _ := h.WalletRepo.GetByID(ctx, walletA.ID)
	finalB, _ := h.WalletRepo.GetByID(ctx, walletB.ID)
	finalSystemBalance := finalA.Balance + finalB.Balance

	if finalSystemBalance != initialSystemBalance {
		t.Fatalf("Gold was created or destroyed during concurrent transfers! Initial %d != Final %d",
			initialSystemBalance, finalSystemBalance)
	}

	// Invariant 2: Double-entry audit via Reconciliation Service
	report, err := h.ReconService.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconciliation audit failed: %v", err)
	}

	if !report.IsSystemBalanced {
		t.Errorf("System failed reconciliation audit after concurrent transfers: %+v", report)
	}
	if report.MismatchedWallets > 0 {
		t.Errorf("Reconciliation detected %d mismatched wallets: %+v", report.MismatchedWallets, report.Discrepancies)
	}
}

// TestDoubleEntryLedger_ReconciliationDetectsInconsistency verifies that the reconciliation
// mechanism accurately flags any discrepancy between a wallet balance and its ledger history.
func TestDoubleEntryLedger_ReconciliationDetectsInconsistency(t *testing.T) {
	h := SetupTestHarness(t)
	ctx := context.Background()

	user1 := uuid.New()
	user2 := uuid.New()
	sender := h.CreateTestWallet(t, user1, 1000)
	receiver := h.CreateTestWallet(t, user2, 500)

	// Valid transfer
	_, err := h.WalletService.TransferMoney(ctx, "recon-clean-001", user1, sender.ID, receiver.ID, 200)
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// 1. Initial reconciliation should pass with 100% matched wallets
	cleanReport, err := h.ReconService.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Clean reconciliation failed: %v", err)
	}
	if !cleanReport.IsSystemBalanced || cleanReport.MismatchedWallets != 0 {
		t.Fatalf("Expected system to be balanced, got report: %+v", cleanReport)
	}

	// 2. Tamper: Manually mutate sender wallet balance in database to simulate silent data corruption
	tamperedBalance := int64(9999) // Incorrect balance
	_ = h.WalletRepo.UpdateBalance(ctx, nil, sender.ID, tamperedBalance)

	// 3. Run reconciliation audit again
	tamperedReport, err := h.ReconService.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconciliation query failed: %v", err)
	}

	// Audit MUST detect the discrepancy!
	if tamperedReport.IsSystemBalanced {
		t.Errorf("Reconciliation failed to catch tampered wallet balance!")
	}
	if tamperedReport.MismatchedWallets != 1 {
		t.Errorf("Expected 1 mismatched wallet, found %d", tamperedReport.MismatchedWallets)
	}
	if len(tamperedReport.Discrepancies) == 0 {
		t.Fatalf("Expected discrepancy details, found none")
	}

	discrepancy := tamperedReport.Discrepancies[0]
	if discrepancy.WalletID != sender.ID {
		t.Errorf("Expected flagged wallet %s, got %s", sender.ID, discrepancy.WalletID)
	}
	if discrepancy.WalletBalance != tamperedBalance {
		t.Errorf("Expected audited wallet balance %d, got %d", tamperedBalance, discrepancy.WalletBalance)
	}
	expectedLedgerBalance := int64(800) // 1000 - 200
	if discrepancy.LedgerBalance != expectedLedgerBalance {
		t.Errorf("Expected derived ledger balance %d, got %d", expectedLedgerBalance, discrepancy.LedgerBalance)
	}
}

