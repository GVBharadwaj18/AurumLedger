package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"aurumledger/internal/domain"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type cacheRepository struct {
	client *redis.Client
}

func NewCacheRepository(client *redis.Client) domain.CacheRepository {
	return &cacheRepository{client: client}
}

func (r *cacheRepository) GetWallet(ctx context.Context, walletID uuid.UUID) (*domain.Wallet, error) {
	if r.client == nil {
		return nil, nil // No cache configured
	}
	key := fmt.Sprintf("wallet:%s", walletID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // Cache miss
	}
	if err != nil {
		return nil, err
	}

	var wallet domain.Wallet
	if err := json.Unmarshal([]byte(val), &wallet); err != nil {
		return nil, err
	}
	return &wallet, nil
}

func (r *cacheRepository) SetWallet(ctx context.Context, wallet *domain.Wallet) error {
	if r.client == nil || wallet == nil {
		return nil
	}
	key := fmt.Sprintf("wallet:%s", wallet.ID)
	data, err := json.Marshal(wallet)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, key, data, 10*time.Minute).Err()
}

func (r *cacheRepository) InvalidateWallet(ctx context.Context, walletID uuid.UUID) error {
	if r.client == nil {
		return nil
	}
	key := fmt.Sprintf("wallet:%s", walletID)
	return r.client.Del(ctx, key).Err()
}
