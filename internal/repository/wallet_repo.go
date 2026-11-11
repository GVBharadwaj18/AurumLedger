package repository

import (
	"context"

	"aurumledger/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type walletRepository struct {
	db *gorm.DB
}

func NewWalletRepository(db *gorm.DB) domain.WalletRepository {
	return &walletRepository{db: db}
}

func (r *walletRepository) Create(ctx context.Context, tx *gorm.DB, wallet *domain.Wallet) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Create(wallet).Error
}

func (r *walletRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Wallet, error) {
	var wallets []domain.Wallet
	if err := r.db.WithContext(ctx).Where("id = ?", id).Limit(1).Find(&wallets).Error; err != nil {
		return nil, err
	}
	if len(wallets) == 0 {
		return nil, domain.ErrWalletNotFound
	}
	return &wallets[0], nil
}

func (r *walletRepository) GetByIDWithLock(ctx context.Context, tx *gorm.DB, id uuid.UUID) (*domain.Wallet, error) {
	conn := r.db
	if tx != nil {
		conn = tx
	}

	// Use SELECT FOR UPDATE for row-level locking
	query := conn.WithContext(ctx)
	// SQLite in memory doesn't use FOR UPDATE, so we check dialect
	if conn.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var wallets []domain.Wallet
	if err := query.Where("id = ?", id).Limit(1).Find(&wallets).Error; err != nil {
		return nil, err
	}
	if len(wallets) == 0 {
		return nil, domain.ErrWalletNotFound
	}
	return &wallets[0], nil
}

func (r *walletRepository) UpdateBalance(ctx context.Context, tx *gorm.DB, id uuid.UUID, newBalance int64) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Model(&domain.Wallet{}).Where("id = ?", id).Update("balance", newBalance).Error
}

func (r *walletRepository) GetAll(ctx context.Context) ([]domain.Wallet, error) {
	var wallets []domain.Wallet
	if err := r.db.WithContext(ctx).Order("created_at ASC").Find(&wallets).Error; err != nil {
		return nil, err
	}
	return wallets, nil
}

func (r *walletRepository) WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}
