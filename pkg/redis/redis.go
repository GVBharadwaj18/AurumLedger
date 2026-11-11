package redis

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/redis/go-redis/v9"
)

func NewClient(addr string, password string, db int) (*redis.Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis address is empty")
	}

	var opt *redis.Options

	// Support full URI schemes: redis://... or rediss://... (Upstash, AWS ElastiCache, Render)
	if strings.HasPrefix(addr, "redis://") || strings.HasPrefix(addr, "rediss://") {
		parsedOpt, err := redis.ParseURL(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redis url: %w", err)
		}
		opt = parsedOpt
	} else {
		opt = &redis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		}
	}

	client := redis.NewClient(opt)

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	log.Println("Connected to Redis")
	return client, nil
}
