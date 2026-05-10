package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// ResponseCache caches LLM responses in Redis.
// The cache key must already include tenant_id (built by ai_service).
type ResponseCache struct {
	rdb        *redis.Client
	defaultTTL time.Duration
}

// IdempotencyCache stores idempotency keys per tenant (TTL 5 min).
type IdempotencyCache struct {
	rdb *redis.Client
}

// New creates a Redis client. Returns nil if redisURL is empty (no-op mode).
func NewRedisClient(redisURL string) *redis.Client {
	if redisURL == "" {
		logrus.Info("cache: REDIS_URL not set — cache disabled (no-op mode)")
		return nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		logrus.WithError(err).Warn("cache: invalid REDIS_URL — no-op mode")
		return nil
	}
	return redis.NewClient(opt)
}

// NewResponseCache creates a ResponseCache. rdb may be nil (no-op).
func NewResponseCache(rdb *redis.Client, defaultTTLSeconds int) *ResponseCache {
	return &ResponseCache{rdb: rdb, defaultTTL: time.Duration(defaultTTLSeconds) * time.Second}
}

func (c *ResponseCache) Get(ctx context.Context, key string) (*provider.CompletionResponse, bool) {
	if c.rdb == nil {
		return nil, false
	}
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var resp provider.CompletionResponse
	if json.Unmarshal(data, &resp) != nil {
		return nil, false
	}
	return &resp, true
}

func (c *ResponseCache) Set(ctx context.Context, key string, resp *provider.CompletionResponse, ttl time.Duration) {
	if c.rdb == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if ttl <= 0 {
		ttl = c.defaultTTL
	}
	_ = c.rdb.Set(ctx, key, data, ttl).Err()
}

// NewIdempotencyCache creates an IdempotencyCache. rdb may be nil (no-op → no idempotency).
func NewIdempotencyCache(rdb *redis.Client) *IdempotencyCache {
	return &IdempotencyCache{rdb: rdb}
}

func (c *IdempotencyCache) idemKey(tenantID, key string) string {
	return fmt.Sprintf("ai:idem:%s:%s", tenantID, key)
}

func (c *IdempotencyCache) Get(ctx context.Context, tenantID, key string) (*provider.CompletionResponse, bool) {
	if c.rdb == nil {
		return nil, false
	}
	data, err := c.rdb.Get(ctx, c.idemKey(tenantID, key)).Bytes()
	if err != nil {
		return nil, false
	}
	var resp provider.CompletionResponse
	if json.Unmarshal(data, &resp) != nil {
		return nil, false
	}
	return &resp, true
}

func (c *IdempotencyCache) Set(ctx context.Context, tenantID, key string, resp *provider.CompletionResponse) {
	if c.rdb == nil {
		return
	}
	data, _ := json.Marshal(resp)
	_ = c.rdb.Set(ctx, c.idemKey(tenantID, key), data, 5*time.Minute).Err()
}
