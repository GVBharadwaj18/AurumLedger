package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Wallet represents a user's digital gold wallet.
// Balance is stored in milligrams (mg) of fine 24K gold (1,000 mg = 1 gram).
type Wallet struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	Name      string    `gorm:"size:100;default:''" json:"name"`
	Balance   int64     `gorm:"not null;default:0;check:balance >= 0" json:"balance"` // In milligrams of 24K gold, >= 0
	Currency  string    `gorm:"size:10;not null;default:'XAU_MG'" json:"currency"`   // XAU_MG = Milligram gold
	Status    string    `gorm:"size:20;not null;default:'ACTIVE'" json:"status"`     // ACTIVE, FROZEN
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// IdempotencyKey stores persistent idempotency records in PostgreSQL (Priority 1).
// Unique constraint on (user_id, key) prevents duplicate logical execution.
type IdempotencyKey struct {
	ID                 uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	Key                string    `gorm:"size:128;not null;index:idx_user_key,unique" json:"key"`
	UserID             uuid.UUID `gorm:"type:uuid;not null;index:idx_user_key,unique" json:"user_id"`
	RequestFingerprint string    `gorm:"size:64;not null" json:"request_fingerprint"` // SHA-256 hex string
	Status             string    `gorm:"size:20;not null" json:"status"`              // PENDING, COMPLETED, FAILED
	ResponseCode       int       `gorm:"default:0" json:"response_code"`
	ResponseBody       string    `gorm:"type:text" json:"response_body"`
	CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	ExpiresAt          time.Time `gorm:"not null" json:"expires_at"`
}

// Transaction represents the business transfer record.
type Transaction struct {
	ID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	SenderID   *uuid.UUID `gorm:"type:uuid;index" json:"sender_id,omitempty"`
	ReceiverID *uuid.UUID `gorm:"type:uuid;index" json:"receiver_id,omitempty"`
	Amount     int64      `gorm:"not null;check:amount > 0" json:"amount"` // mg of gold
	Type       string     `gorm:"size:20;not null" json:"type"`             // TRANSFER, DEPOSIT, WITHDRAWAL
	Status     string     `gorm:"size:20;not null;default:'COMPLETED'" json:"status"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
}

// LedgerEntry represents an immutable double-entry accounting record (Priority 2).
// In double-entry gold accounting:
// Every transfer generates:
// - One CREDIT entry for sender (reduces asset balance)
// - One DEBIT entry for receiver (increases asset balance)
// Sum of debits must equal sum of credits for every transaction.
type LedgerEntry struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	TransactionID uuid.UUID `gorm:"type:uuid;not null;index" json:"transaction_id"`
	WalletID      uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
	EntryType     string    `gorm:"size:10;not null;check:entry_type IN ('DEBIT','CREDIT')" json:"entry_type"` // DEBIT (+), CREDIT (-)
	Amount        int64     `gorm:"not null;check:amount > 0" json:"amount"`                                    // mg of gold, strictly positive
	BalanceAfter  int64     `gorm:"not null;check:balance_after >= 0" json:"balance_after"`                     // Running balance snapshot
	Description   string    `gorm:"size:255" json:"description"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"` // Immutable timestamp
}

// OutboxEvent represents an event to be published via the Transactional Outbox Pattern (Priority 3).
type OutboxEvent struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	AggregateType string     `gorm:"size:50;not null" json:"aggregate_type"` // e.g., "WALLET_TRANSFER"
	AggregateID   uuid.UUID  `gorm:"type:uuid;not null;index" json:"aggregate_id"`
	EventType     string     `gorm:"size:50;not null" json:"event_type"` // e.g., "TRANSFER_COMPLETED"
	Payload       string     `gorm:"type:text;not null" json:"payload"`  // JSON payload
	Status        string     `gorm:"size:20;not null;index:idx_outbox_status_created" json:"status"` // PENDING, PUBLISHED, FAILED
	RetryCount    int        `gorm:"not null;default:0" json:"retry_count"`
	LastError     *string    `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt     time.Time  `gorm:"autoCreateTime;index:idx_outbox_status_created" json:"created_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

// ProcessedEvent tracks message consumption idempotently (Priority 4).
// Guarantees at database level that redelivered messages are not processed twice.
type ProcessedEvent struct {
	EventID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"event_id"`
	EventType    string    `gorm:"size:50;not null" json:"event_type"`
	ConsumerName string    `gorm:"size:50;not null" json:"consumer_name"`
	ProcessedAt  time.Time `gorm:"autoCreateTime" json:"processed_at"`
	Status       string    `gorm:"size:20;not null;default:'SUCCESS'" json:"status"`
}

// TransferEvent is the payload published to RabbitMQ.
type TransferEvent struct {
	EventID       uuid.UUID `json:"event_id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	SenderID      uuid.UUID `json:"sender_id"`
	ReceiverID    uuid.UUID `json:"receiver_id"`
	Amount        int64     `json:"amount"` // mg of gold
	Timestamp     time.Time `json:"timestamp"`
}

// Reconciliation models
type WalletDiscrepancy struct {
	WalletID       uuid.UUID `json:"wallet_id"`
	WalletBalance  int64     `json:"wallet_balance"`
	LedgerBalance  int64     `json:"ledger_balance"`
	Difference     int64     `json:"difference"`
}

type ReconciliationReport struct {
	TotalWallets         int                 `json:"total_wallets"`
	MatchedWallets       int                 `json:"matched_wallets"`
	MismatchedWallets     int                 `json:"mismatched_wallets"`
	SystemTotalBalance   int64               `json:"system_total_balance"`
	SystemTotalDebits    int64               `json:"system_total_debits"`
	SystemTotalCredits   int64               `json:"system_total_credits"`
	IsSystemBalanced     bool                `json:"is_system_balanced"`
	Discrepancies        []WalletDiscrepancy `json:"discrepancies,omitempty"`
	AuditedAt            time.Time           `json:"audited_at"`
}

// Repository Interfaces

type WalletRepository interface {
	Create(ctx context.Context, tx *gorm.DB, wallet *Wallet) error
	GetByID(ctx context.Context, id uuid.UUID) (*Wallet, error)
	GetByIDWithLock(ctx context.Context, tx *gorm.DB, id uuid.UUID) (*Wallet, error)
	UpdateBalance(ctx context.Context, tx *gorm.DB, id uuid.UUID, newBalance int64) error
	GetAll(ctx context.Context) ([]Wallet, error)
	WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error
}

type IdempotencyRepository interface {
	Get(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID) (*IdempotencyKey, error)
	Reserve(ctx context.Context, tx *gorm.DB, record *IdempotencyKey) error
	Complete(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID, code int, responseBody string) error
	Fail(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID, responseBody string) error
}

type LedgerRepository interface {
	CreateEntries(ctx context.Context, tx *gorm.DB, entries []LedgerEntry) error
	GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]LedgerEntry, error)
	GetEntriesByWallet(ctx context.Context, walletID uuid.UUID) ([]LedgerEntry, error)
	GetRecentEntries(ctx context.Context, limit int) ([]LedgerEntry, error)
	CalculateLedgerBalance(ctx context.Context, walletID uuid.UUID) (int64, error)
	GetTotalSystemDebitsAndCredits(ctx context.Context) (totalDebits int64, totalCredits int64, err error)
}

type TransactionRepository interface {
	Create(ctx context.Context, tx *gorm.DB, transaction *Transaction) error
	GetByID(ctx context.Context, id uuid.UUID) (*Transaction, error)
}

type OutboxRepository interface {
	Create(ctx context.Context, tx *gorm.DB, event *OutboxEvent) error
	FetchPending(ctx context.Context, tx *gorm.DB, batchSize int) ([]OutboxEvent, error)
	GetRecent(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string, retryCount int) error
}

type ProcessedEventRepository interface {
	IsProcessed(ctx context.Context, eventID uuid.UUID) (bool, error)
	MarkProcessed(ctx context.Context, tx *gorm.DB, eventID uuid.UUID, eventType, consumerName string) error
}

type CacheRepository interface {
	GetWallet(ctx context.Context, walletID uuid.UUID) (*Wallet, error)
	SetWallet(ctx context.Context, wallet *Wallet) error
	InvalidateWallet(ctx context.Context, walletID uuid.UUID) error
}

type EventProducer interface {
	PublishTransferEvent(ctx context.Context, event TransferEvent) error
}
