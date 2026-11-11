package repository

import (
	"context"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ledgerRepository struct {
	db *gorm.DB
}

func NewLedgerRepository(db *gorm.DB) domain.LedgerRepository {
	return &ledgerRepository{db: db}
}

func (r *ledgerRepository) CreateEntries(ctx context.Context, tx *gorm.DB, entries []domain.LedgerEntry) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Create(&entries).Error
}

func (r *ledgerRepository) GetEntriesByTransaction(ctx context.Context, txID uuid.UUID) ([]domain.LedgerEntry, error) {
	var entries []domain.LedgerEntry
	if err := r.db.WithContext(ctx).Where("transaction_id = ?", txID).Order("created_at ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *ledgerRepository) GetEntriesByWallet(ctx context.Context, walletID uuid.UUID) ([]domain.LedgerEntry, error) {
	var entries []domain.LedgerEntry
	if err := r.db.WithContext(ctx).Where("wallet_id = ?", walletID).Order("created_at ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *ledgerRepository) CalculateLedgerBalance(ctx context.Context, walletID uuid.UUID) (int64, error) {
	type Result struct {
		TotalDebits  int64 `gorm:"column:total_debits"`
		TotalCredits int64 `gorm:"column:total_credits"`
	}

	var res Result
	// DEBIT increases gold balance (inflow), CREDIT decreases gold balance (outflow)
	err := r.db.WithContext(ctx).Model(&domain.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN entry_type = 'DEBIT' THEN amount ELSE 0 END), 0) as total_debits, "+
			"COALESCE(SUM(CASE WHEN entry_type = 'CREDIT' THEN amount ELSE 0 END), 0) as total_credits").
		Where("wallet_id = ?", walletID).
		Scan(&res).Error

	if err != nil {
		return 0, err
	}

	return res.TotalDebits - res.TotalCredits, nil
}

func (r *ledgerRepository) GetTotalSystemDebitsAndCredits(ctx context.Context) (int64, int64, error) {
	type Result struct {
		TotalDebits  int64 `gorm:"column:total_debits"`
		TotalCredits int64 `gorm:"column:total_credits"`
	}

	var res Result
	err := r.db.WithContext(ctx).Model(&domain.LedgerEntry{}).
		Select("COALESCE(SUM(CASE WHEN entry_type = 'DEBIT' THEN amount ELSE 0 END), 0) as total_debits, "+
			"COALESCE(SUM(CASE WHEN entry_type = 'CREDIT' THEN amount ELSE 0 END), 0) as total_credits").
		Scan(&res).Error

	if err != nil {
		return 0, 0, err
	}

	return res.TotalDebits, res.TotalCredits, nil
}

func (r *ledgerRepository) GetRecentEntries(ctx context.Context, limit int) ([]domain.LedgerEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	var entries []domain.LedgerEntry
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}


