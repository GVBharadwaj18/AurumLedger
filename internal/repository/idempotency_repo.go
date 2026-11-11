package repository

import (
	"context"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type idempotencyRepository struct {
	db *gorm.DB
}

func NewIdempotencyRepository(db *gorm.DB) domain.IdempotencyRepository {
	return &idempotencyRepository{db: db}
}

func (r *idempotencyRepository) Get(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID) (*domain.IdempotencyKey, error) {
	conn := r.db
	if tx != nil {
		conn = tx
	}

	query := conn.WithContext(ctx)
	if conn.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var records []domain.IdempotencyKey
	if err := query.Where("key = ? AND user_id = ?", key, userID).Limit(1).Find(&records).Error; err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &records[0], nil
}

func (r *idempotencyRepository) Reserve(ctx context.Context, tx *gorm.DB, record *domain.IdempotencyKey) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Create(record).Error
}

func (r *idempotencyRepository) Complete(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID, code int, responseBody string) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Model(&domain.IdempotencyKey{}).
		Where("key = ? AND user_id = ?", key, userID).
		Updates(map[string]interface{}{
			"status":        "COMPLETED",
			"response_code": code,
			"response_body": responseBody,
			"updated_at":    time.Now().UTC(),
		}).Error
}

func (r *idempotencyRepository) Fail(ctx context.Context, tx *gorm.DB, key string, userID uuid.UUID, responseBody string) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Model(&domain.IdempotencyKey{}).
		Where("key = ? AND user_id = ?", key, userID).
		Updates(map[string]interface{}{
			"status":        "FAILED",
			"response_body": responseBody,
			"updated_at":    time.Now().UTC(),
		}).Error
}

