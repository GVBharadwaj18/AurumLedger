package postgres

import (
	"fmt"
	"log"
	"strings"
	"time"

	"aurumledger/internal/domain"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewConnection(dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	isSQLite := strings.HasPrefix(dsn, "sqlite")

	if isSQLite {
		dbPath := strings.TrimPrefix(dsn, "sqlite://")
		if dbPath == "" {
			dbPath = "aurumledger.db"
		}
		dialector = sqlite.Open(dbPath)
	} else {
		dialector = postgres.Open(dsn)
	}

	gormLogger := logger.New(
		log.New(log.Writer(), "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if isSQLite {
		sqlDB.SetMaxOpenConns(1)
	} else {
		// Optimize connection pooling for high concurrency in PostgreSQL
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(25)
		sqlDB.SetConnMaxLifetime(15 * time.Minute)
	}

	// Auto-migrate all AurumLedger schemas
	err = db.AutoMigrate(
		&domain.Wallet{},
		&domain.Transaction{},
		&domain.IdempotencyKey{},
		&domain.LedgerEntry{},
		&domain.OutboxEvent{},
		&domain.ProcessedEvent{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	if isSQLite {
		log.Println("Connected to embedded SQLite database & AurumLedger schemas migrated")
	} else {
		log.Println("Connected to PostgreSQL & AurumLedger schemas migrated successfully")
	}

	return db, nil
}
