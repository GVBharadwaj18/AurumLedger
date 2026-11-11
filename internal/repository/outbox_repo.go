package repository

import (
	"context"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type outboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) domain.OutboxRepository {
	return &outboxRepository{db: db}
}

func (r *outboxRepository) Create(ctx context.Context, tx *gorm.DB, event *domain.OutboxEvent) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}
	return conn.WithContext(ctx).Create(event).Error
}

func (r *outboxRepository) FetchPending(ctx context.Context, tx *gorm.DB, batchSize int) ([]domain.OutboxEvent, error) {
	conn := r.db
	if tx != nil {
		conn = tx
	}

	var claimedEvents []domain.OutboxEvent

	// Atomic claim pattern: select pending events and mark them PROCESSING atomically
	// On PostgreSQL: SELECT ... FOR UPDATE SKIP LOCKED
	// On SQLite: Transactional claim to prevent multiple workers from acquiring the same row
	err := conn.WithContext(ctx).Transaction(func(claimTx *gorm.DB) error {
		query := claimTx.Model(&domain.OutboxEvent{}).
			Where("status = ?", "PENDING").
			Order("created_at ASC").
			Limit(batchSize)

		if claimTx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{
				Strength: "UPDATE",
				Options:  "SKIP LOCKED",
			})
		}

		var candidates []domain.OutboxEvent
		if err := query.Find(&candidates).Error; err != nil {
			return err
		}

		if len(candidates) == 0 {
			return nil
		}

		// Atomically claim candidate IDs
		ids := make([]uuid.UUID, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
		}

		res := claimTx.Model(&domain.OutboxEvent{}).
			Where("id IN ? AND status = ?", ids, "PENDING").
			Update("status", "PROCESSING")
		if res.Error != nil {
			return res.Error
		}

		// Re-fetch only the successfully claimed rows
		if err := claimTx.Where("id IN ? AND status = ?", ids, "PROCESSING").Find(&claimedEvents).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return claimedEvents, nil
}

func (r *outboxRepository) MarkPublished(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&domain.OutboxEvent{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       "PUBLISHED",
			"published_at": &now,
			"last_error":   nil,
		}).Error
}

func (r *outboxRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string, retryCount int) error {
	status := "PENDING" // Reset to PENDING so next retry cycle can pick it up
	if retryCount >= 10 {
		status = "FAILED" // Terminal failure after max publisher retries
	}
	return r.db.WithContext(ctx).Model(&domain.OutboxEvent{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"retry_count": retryCount,
			"last_error":  &errMsg,
		}).Error
}

func (r *outboxRepository) GetRecent(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	var events []domain.OutboxEvent
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

