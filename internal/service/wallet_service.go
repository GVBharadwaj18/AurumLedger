package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"aurumledger/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WalletService struct {
	walletRepo      domain.WalletRepository
	transRepo       domain.TransactionRepository
	idempotencyRepo domain.IdempotencyRepository
	ledgerRepo      domain.LedgerRepository
	outboxRepo      domain.OutboxRepository
	cacheRepo       domain.CacheRepository
}

func NewWalletService(
	wRepo domain.WalletRepository,
	tRepo domain.TransactionRepository,
	iRepo domain.IdempotencyRepository,
	lRepo domain.LedgerRepository,
	oRepo domain.OutboxRepository,
	cRepo domain.CacheRepository,
) *WalletService {
	return &WalletService{
		walletRepo:      wRepo,
		transRepo:       tRepo,
		idempotencyRepo: iRepo,
		ledgerRepo:      lRepo,
		outboxRepo:      oRepo,
		cacheRepo:       cRepo,
	}
}

// CreateWallet initializes a new digital gold wallet.
func (s *WalletService) CreateWallet(ctx context.Context, userID uuid.UUID, initialBalance int64) (*domain.Wallet, error) {
	return s.CreateWalletWithName(ctx, userID, "Wallet", initialBalance)
}

// CreateWalletWithName initializes a new digital gold wallet with a custom human-readable name.
func (s *WalletService) CreateWalletWithName(ctx context.Context, userID uuid.UUID, name string, initialBalance int64) (*domain.Wallet, error) {
	if initialBalance < 0 {
		return nil, domain.ErrInvalidAmount
	}

	if name == "" {
		name = "Digital Gold Wallet"
	}

	wallet := &domain.Wallet{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      name,
		Balance:   initialBalance,
		Currency:  "XAU_MG", // Milligrams of 24K gold
		Status:    "ACTIVE",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err := s.walletRepo.WithTx(ctx, func(tx *gorm.DB) error {
		if err := s.walletRepo.Create(ctx, tx, wallet); err != nil {
			return err
		}

		// If initial balance > 0, generate initial deposit transaction and ledger entry
		if initialBalance > 0 {
			txRecord := &domain.Transaction{
				ID:         uuid.New(),
				ReceiverID: &wallet.ID,
				Amount:     initialBalance,
				Type:       "DEPOSIT",
				Status:     "COMPLETED",
				CreatedAt:  time.Now().UTC(),
			}
			if err := s.transRepo.Create(ctx, tx, txRecord); err != nil {
				return err
			}

			// In double-entry: debit the wallet asset account
			entry := domain.LedgerEntry{
				ID:            uuid.New(),
				TransactionID: txRecord.ID,
				WalletID:      wallet.ID,
				EntryType:     "DEBIT",
				Amount:        initialBalance,
				BalanceAfter:  initialBalance,
				Description:   "Initial gold deposit",
				CreatedAt:     time.Now().UTC(),
			}
			if err := s.ledgerRepo.CreateEntries(ctx, tx, []domain.LedgerEntry{entry}); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return wallet, nil
}

// GetBalance retrieves a wallet with Cache-Aside pattern (Redis -> DB).
func (s *WalletService) GetBalance(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error) {
	// 1. Check Cache
	if s.cacheRepo != nil {
		cached, err := s.cacheRepo.GetWallet(ctx, walletID)
		if err == nil && cached != nil {
			return cached, nil
		}
	}

	// 2. Fetch from Database
	wallet, err := s.walletRepo.GetByID(ctx, walletID)
	if err != nil {
		return nil, err
	}

	// 3. Update Cache
	if s.cacheRepo != nil {
		if err := s.cacheRepo.SetWallet(ctx, wallet); err != nil {
			log.Printf("failed to set cache for wallet %s: %v", walletID, err)
		}
	}

	return wallet, nil
}

// ComputeRequestFingerprint creates a deterministic SHA-256 hash of the transfer parameters.
func ComputeRequestFingerprint(senderID, receiverID uuid.UUID, amount int64) string {
	hasher := sha256.New()
	hasher.Write([]byte(fmt.Sprintf("%s:%s:%d", senderID.String(), receiverID.String(), amount)))
	return hex.EncodeToString(hasher.Sum(nil))
}

type TransferResponse struct {
	Transaction          *domain.Transaction `json:"transaction"`
	IsIdempotentReplay   bool                `json:"is_idempotent_replay"`
	SenderBalanceAfter   int64               `json:"sender_balance_after"`
	ReceiverBalanceAfter int64               `json:"receiver_balance_after"`
}

// TransferMoney executes the complete 15-step gold transfer lifecycle.
// Enforces:
// 1. Persistent Idempotency (Priority 1)
// 2. Double-Entry Ledger Accounting (Priority 2)
// 3. Transactional Outbox Pattern (Priority 3)
// 4. Concurrency protection via deterministic row locking.
func (s *WalletService) TransferMoney(
	ctx context.Context,
	idempotencyKey string,
	authenticatedUserID uuid.UUID,
	senderID, receiverID uuid.UUID,
	amount int64,
) (*TransferResponse, error) {
	if idempotencyKey == "" {
		return nil, domain.ErrIdempotencyKeyRequired
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	if senderID == receiverID {
		return nil, domain.ErrSelfTransfer
	}

	fingerprint := ComputeRequestFingerprint(senderID, receiverID, amount)

	var (
		resultTransaction  domain.Transaction
		isIdempotentReplay bool
		senderBalAfter     int64
		receiverBalAfter   int64
	)

	// Step 3: Begin PostgreSQL Database Transaction
	err := s.walletRepo.WithTx(ctx, func(tx *gorm.DB) error {
		// Step 4: Check Idempotency Key
		existingKey, err := s.idempotencyRepo.Get(ctx, tx, idempotencyKey, authenticatedUserID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to check idempotency key: %w", err)
		}

		if existingKey != nil {
			// Reject reuse of same key with different payload
			if existingKey.RequestFingerprint != fingerprint {
				return domain.ErrIdempotencyPayloadMismatch
			}

			// If already completed, return original transaction result without re-executing
			if existingKey.Status == "COMPLETED" {
				if err := json.Unmarshal([]byte(existingKey.ResponseBody), &resultTransaction); err != nil {
					return fmt.Errorf("failed to parse cached transaction response: %w", err)
				}
				isIdempotentReplay = true
				return nil // Commit/exit without financial mutations
			}

			// If currently in-flight
			if existingKey.Status == "PENDING" {
				return domain.ErrIdempotencyConflict
			}
		} else {
			// Reserve idempotency key atomically with PENDING status
			newRecord := &domain.IdempotencyKey{
				ID:                 uuid.New(),
				Key:                idempotencyKey,
				UserID:             authenticatedUserID,
				RequestFingerprint: fingerprint,
				Status:             "PENDING",
				ExpiresAt:          time.Now().UTC().Add(24 * time.Hour),
				CreatedAt:          time.Now().UTC(),
				UpdatedAt:          time.Now().UTC(),
			}
			if err := s.idempotencyRepo.Reserve(ctx, tx, newRecord); err != nil {
				// Handle concurrent attempt race condition
				return fmt.Errorf("failed to reserve idempotency key: %w", err)
			}
		}

		// Step 5: Deterministic Row Locking (Deadlock Prevention)
		firstID, secondID := senderID, receiverID
		if firstID.String() > secondID.String() {
			firstID, secondID = receiverID, senderID
		}

		w1, err := s.walletRepo.GetByIDWithLock(ctx, tx, firstID)
		if err != nil {
			return fmt.Errorf("failed to lock wallet %s: %w", firstID, err)
		}
		w2, err := s.walletRepo.GetByIDWithLock(ctx, tx, secondID)
		if err != nil {
			return fmt.Errorf("failed to lock wallet %s: %w", secondID, err)
		}

		var sender, receiver *domain.Wallet
		if w1.ID == senderID {
			sender, receiver = w1, w2
		} else {
			sender, receiver = w2, w1
		}

		// Step 2 & 5: Validate wallet ownership and active status
		if sender.UserID != authenticatedUserID {
			return domain.ErrUnauthorizedWallet
		}
		if sender.Status != "ACTIVE" || receiver.Status != "ACTIVE" {
			return domain.ErrWalletFrozen
		}

		// Step 6: Validate available gold balance
		if sender.Balance < amount {
			return domain.ErrInsufficientFunds
		}

		// Step 7: Atomically update wallet balances
		senderBalAfter = sender.Balance - amount
		receiverBalAfter = receiver.Balance + amount

		if err := s.walletRepo.UpdateBalance(ctx, tx, sender.ID, senderBalAfter); err != nil {
			return fmt.Errorf("failed to update sender balance: %w", err)
		}
		if err := s.walletRepo.UpdateBalance(ctx, tx, receiver.ID, receiverBalAfter); err != nil {
			return fmt.Errorf("failed to update receiver balance: %w", err)
		}

		// Step 9: Create Financial Transaction Record
		txRecord := &domain.Transaction{
			ID:         uuid.New(),
			SenderID:   &sender.ID,
			ReceiverID: &receiver.ID,
			Amount:     amount,
			Type:       "TRANSFER",
			Status:     "COMPLETED",
			CreatedAt:  time.Now().UTC(),
		}
		if err := s.transRepo.Create(ctx, tx, txRecord); err != nil {
			return fmt.Errorf("failed to create transaction record: %w", err)
		}

		// Step 8: Create Double-Entry Ledger Records (Immutable)
		// Receiver is DEBITED (gold asset inflow: +amount)
		// Sender is CREDITED (gold asset outflow: -amount)
		// Invariant: sum of debits == sum of credits
		senderEntry := domain.LedgerEntry{
			ID:            uuid.New(),
			TransactionID: txRecord.ID,
			WalletID:      sender.ID,
			EntryType:     "CREDIT",
			Amount:        amount,
			BalanceAfter:  senderBalAfter,
			Description:   fmt.Sprintf("Gold transfer outflow to wallet %s", receiver.ID),
			CreatedAt:     time.Now().UTC(),
		}

		receiverEntry := domain.LedgerEntry{
			ID:            uuid.New(),
			TransactionID: txRecord.ID,
			WalletID:      receiver.ID,
			EntryType:     "DEBIT",
			Amount:        amount,
			BalanceAfter:  receiverBalAfter,
			Description:   fmt.Sprintf("Gold transfer inflow from wallet %s", sender.ID),
			CreatedAt:     time.Now().UTC(),
		}

		if err := s.ledgerRepo.CreateEntries(ctx, tx, []domain.LedgerEntry{senderEntry, receiverEntry}); err != nil {
			return fmt.Errorf("failed to write double-entry ledger records: %w", err)
		}

		// Step 10: Insert Transactional Outbox Event (Priority 3)
		eventPayload := domain.TransferEvent{
			EventID:       uuid.New(),
			TransactionID: txRecord.ID,
			SenderID:      sender.ID,
			ReceiverID:    receiver.ID,
			Amount:        amount,
			Timestamp:     time.Now().UTC(),
		}
		payloadBytes, err := json.Marshal(eventPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal outbox event payload: %w", err)
		}

		outboxEvent := &domain.OutboxEvent{
			ID:            uuid.New(),
			AggregateType: "WALLET_TRANSFER",
			AggregateID:   txRecord.ID,
			EventType:     "TransferCompleted",
			Payload:       string(payloadBytes),
			Status:        "PENDING",
			RetryCount:    0,
			CreatedAt:     time.Now().UTC(),
		}
		if err := s.outboxRepo.Create(ctx, tx, outboxEvent); err != nil {
			return fmt.Errorf("failed to record outbox event: %w", err)
		}

		// Complete Idempotency record with transaction response
		txBytes, _ := json.Marshal(txRecord)
		if err := s.idempotencyRepo.Complete(ctx, tx, idempotencyKey, authenticatedUserID, http.StatusOK, string(txBytes)); err != nil {
			return fmt.Errorf("failed to complete idempotency record: %w", err)
		}

		resultTransaction = *txRecord
		// Step 11: Transaction commits here atomically!
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Post-Transaction: Invalidate Cache
	if s.cacheRepo != nil {
		_ = s.cacheRepo.InvalidateWallet(ctx, senderID)
		_ = s.cacheRepo.InvalidateWallet(ctx, receiverID)
	}

	return &TransferResponse{
		Transaction:          &resultTransaction,
		IsIdempotentReplay:   isIdempotentReplay,
		SenderBalanceAfter:   senderBalAfter,
		ReceiverBalanceAfter: receiverBalAfter,
	}, nil
}

// GetLedgerHistory returns the immutable audit trail for a wallet.
func (s *WalletService) GetLedgerHistory(ctx context.Context, walletID uuid.UUID) ([]domain.LedgerEntry, error) {
	return s.ledgerRepo.GetEntriesByWallet(ctx, walletID)
}

// ListWallets returns all wallets in the system.
func (s *WalletService) ListWallets(ctx context.Context) ([]domain.Wallet, error) {
	return s.walletRepo.GetAll(ctx)
}

// GetAllLedgerEntries returns the most recent ledger entries across all transactions.
func (s *WalletService) GetAllLedgerEntries(ctx context.Context, limit int) ([]domain.LedgerEntry, error) {
	return s.ledgerRepo.GetRecentEntries(ctx, limit)
}

// GetRecentOutboxEvents returns recent transactional outbox events.
func (s *WalletService) GetRecentOutboxEvents(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	return s.outboxRepo.GetRecent(ctx, limit)
}
