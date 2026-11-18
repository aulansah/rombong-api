// Package cache wraps Redis behind a small interface.
//
// Redis is an optimization, never the source of truth: every caller must be
// able to continue when these methods return an error.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/aulansah/go-ecommerce-api/internal/config"
)

// Cache is the contract the rest of the application depends on.
type Cache interface {
	// Get decodes the cached JSON value into dest. found is false on a miss.
	Get(ctx context.Context, key string, dest any) (found bool, err error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	// Incr atomically increments a counter that never expires.
	Incr(ctx context.Context, key string) (int64, error)
	// IncrWithTTL increments a counter and starts its TTL on first use.
	IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
	Ping(ctx context.Context) error
}

// RedisCache is the Redis-backed Cache implementation.
type RedisCache struct {
	client *redis.Client
}

// NewRedis creates the client. Timeouts are short on purpose: when Redis is
// slow or down, requests must fall back to PostgreSQL quickly instead of
// queueing behind a dead cache.
func NewRedis(cfg config.RedisConfig) *RedisCache {
	return &RedisCache{client: redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		MaxRetries:   1,
	})}
}

// NewRedisFromClient wraps an existing client (used by tests).
func NewRedisFromClient(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

func (r *RedisCache) Get(ctx context.Context, key string, dest any) (bool, error) {
	raw, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis get %s: %w", key, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, fmt.Errorf("decode cached %s: %w", key, err)
	}
	return true, nil
}

func (r *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	if err := r.client.Set(ctx, key, raw, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}
	return nil
}

func (r *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

func (r *RedisCache) Incr(ctx context.Context, key string) (int64, error) {
	n, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incr %s: %w", key, err)
	}
	return n, nil
}

func (r *RedisCache) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	n, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incr %s: %w", key, err)
	}
	if n == 1 {
		if err := r.client.Expire(ctx, key, ttl).Err(); err != nil {
			return n, fmt.Errorf("redis expire %s: %w", key, err)
		}
	}
	return n, nil
}

func (r *RedisCache) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close releases the connection pool.
func (r *RedisCache) Close() error { return r.client.Close() }
