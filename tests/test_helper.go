package tests

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"aurumledger/internal/domain"
	"aurumledger/internal/repository"
	"aurumledger/internal/service"
	"aurumledger/internal/worker"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MockBroker simulates RabbitMQ with durable queues and Dead-Letter Queue (DLQ).
type MockBroker struct {
	mu          sync.Mutex
	isAvailable bool
	published   []PublishedMessage
	dlq         []PublishedMessage
	onPublish   func(messageID string, body []byte) error
}

type PublishedMessage struct {
	MessageID string
	Body      []byte
	Timestamp time.Time
}

func NewMockBroker() *MockBroker {
	return &MockBroker{
		isAvailable: true,
		published:   make([]PublishedMessage, 0),
		dlq:         make([]PublishedMessage, 0),
	}
}

func (b *MockBroker) Publish(ctx context.Context, messageID string, body []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isAvailable {
		return fmt.Errorf("rabbitmq: connection refused (broker unavailable)")
	}

	if b.onPublish != nil {
		if err := b.onPublish(messageID, body); err != nil {
			return err
		}
	}

	b.published = append(b.published, PublishedMessage{
		MessageID: messageID,
		Body:      body,
		Timestamp: time.Now().UTC(),
	})
	return nil
}

func (b *MockBroker) SetAvailable(available bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.isAvailable = available
}

func (b *MockBroker) GetPublishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func (b *MockBroker) GetPublishedMessages() []PublishedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	res := make([]PublishedMessage, len(b.published))
	copy(res, b.published)
	return res
}

func (b *MockBroker) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = make([]PublishedMessage, 0)
	b.dlq = make([]PublishedMessage, 0)
}

// MockDelivery implements worker.MessageDelivery for unit testing consumer behavior.
type MockDelivery struct {
	Body       []byte
	MessageID  string
	AckCalled  bool
	NackCalled bool
	Requeued   bool
	mu         sync.Mutex
}

func NewMockDelivery(body []byte, messageID string) *MockDelivery {
	return &MockDelivery{
		Body:      body,
		MessageID: messageID,
	}
}

func (m *MockDelivery) GetBody() []byte {
	return m.Body
}

func (m *MockDelivery) GetMessageID() string {
	return m.MessageID
}

func (m *MockDelivery) Ack() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AckCalled = true
	return nil
}

func (m *MockDelivery) Nack(requeue bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NackCalled = true
	m.Requeued = requeue
	return nil
}

// TestHarness bundles test components for easy instantiation.
type TestHarness struct {
	DB              *gorm.DB
	WalletRepo      domain.WalletRepository
	TransRepo       domain.TransactionRepository
	IdempotencyRepo domain.IdempotencyRepository
	LedgerRepo      domain.LedgerRepository
	OutboxRepo      domain.OutboxRepository
	ProcessedRepo   domain.ProcessedEventRepository
	WalletService   *service.WalletService
	ReconService    *service.ReconciliationService
	OutboxPublisher *service.OutboxPublisher
	Worker          *worker.Worker
	Broker          *MockBroker
}

// SetupTestHarness initializes an in-memory SQLite database with GORM and all repositories.
func SetupTestHarness(t *testing.T) *TestHarness {
	// Unique in-memory DB per test
	dbName := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared&_busy_timeout=15000", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to initialize in-memory test database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("Failed to get underlying DB: %v", err)
	}
	// For in-memory SQLite, serialize connections to avoid SQLite "database table is locked" error under concurrent goroutines
	sqlDB.SetMaxOpenConns(1)

	// AutoMigrate all schemas
	err = db.AutoMigrate(
		&domain.Wallet{},
		&domain.Transaction{},
		&domain.IdempotencyKey{},
		&domain.LedgerEntry{},
		&domain.OutboxEvent{},
		&domain.ProcessedEvent{},
	)
	if err != nil {
		t.Fatalf("Failed to auto-migrate schemas: %v", err)
	}

	broker := NewMockBroker()
	walletRepo := repository.NewWalletRepository(db)
	transRepo := repository.NewTransactionRepository(db)
	idempotencyRepo := repository.NewIdempotencyRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	processedRepo := repository.NewProcessedEventRepository(db)
	cacheRepo := repository.NewCacheRepository(nil) // in-memory cache bypass

	walletSvc := service.NewWalletService(walletRepo, transRepo, idempotencyRepo, ledgerRepo, outboxRepo, cacheRepo)
	reconSvc := service.NewReconciliationService(walletRepo, ledgerRepo)
	outboxPub := service.NewOutboxPublisher(outboxRepo, broker, 50*time.Millisecond, 20)
	transferWorker := worker.NewWorker(nil, processedRepo)

	return &TestHarness{
		DB:              db,
		WalletRepo:      walletRepo,
		TransRepo:       transRepo,
		IdempotencyRepo: idempotencyRepo,
		LedgerRepo:      ledgerRepo,
		OutboxRepo:      outboxRepo,
		ProcessedRepo:   processedRepo,
		WalletService:   walletSvc,
		ReconService:    reconSvc,
		OutboxPublisher: outboxPub,
		Worker:          transferWorker,
		Broker:          broker,
	}
}

// CreateTestWallet creates a wallet with initial gold balance.
func (h *TestHarness) CreateTestWallet(t *testing.T, userID uuid.UUID, balance int64) *domain.Wallet {
	ctx := context.Background()
	w, err := h.WalletService.CreateWallet(ctx, userID, balance)
	if err != nil {
		t.Fatalf("Failed to create test wallet: %v", err)
	}
	return w
}
