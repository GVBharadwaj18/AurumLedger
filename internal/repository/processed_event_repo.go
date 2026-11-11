package repository

import (
	"context"
	"errors"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type processedEventRepository struct {
	db *gorm.DB
}

func NewProcessedEventRepository(db *gorm.DB) domain.ProcessedEventRepository {
	return &processedEventRepository{db: db}
}

func (r *processedEventRepository) IsProcessed(ctx context.Context, eventID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.ProcessedEvent{}).
		Where("event_id = ?", eventID).
		Count(&count).Error

	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *processedEventRepository) MarkProcessed(ctx context.Context, tx *gorm.DB, eventID uuid.UUID, eventType, consumerName string) error {
	conn := r.db
	if tx != nil {
		conn = tx
	}

	record := &domain.ProcessedEvent{
		EventID:      eventID,
		EventType:    eventType,
		ConsumerName: consumerName,
		ProcessedAt:  time.Now().UTC(),
		Status:       "SUCCESS",
	}

	err := conn.WithContext(ctx).Create(record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil // Already processed, idempotent success
		}
		return err
	}
	return nil
}

