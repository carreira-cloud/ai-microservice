package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RateLimiter enforces a per-tenant sliding window (1-minute buckets) using Redis.
type RateLimiter struct {
	rdb *redis.Client
	rpm int
}

// NewRateLimiter creates a RateLimiter. If rdb is nil, all requests are allowed (fail-open).
func NewRateLimiter(rdb *redis.Client, rpm int) *RateLimiter {
	return &RateLimiter{rdb: rdb, rpm: rpm}
}

// Allow returns true if the tenant has remaining quota for the current minute.
// Fail-open: returns true when Redis is unavailable.
func (l *RateLimiter) Allow(ctx context.Context, tenantID string) bool {
	if l.rdb == nil {
		return true
	}
	bucket := time.Now().UTC().Format("200601021504")
	key := "ai:rl:" + tenantID + ":" + bucket

	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		logrus.WithError(err).Warn("ratelimit: Redis error — fail-open")
		return true
	}
	if count == 1 {
		l.rdb.Expire(ctx, key, 2*time.Minute) // TTL slightly above 1 min for safety
	}
	return count <= int64(l.rpm)
}

// RateLimit returns a gin middleware that enforces per-tenant rate limiting.
// Must be placed after TenantAuth (requires "tenant_id" in gin.Context).
func RateLimit(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetString("tenant_id")
		if !limiter.Allow(c.Request.Context(), tenantID) {
			retryAfter := 60 - time.Now().Second()
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":                "rate_limit_exceeded",
				"retry_after_seconds": retryAfter,
			})
			return
		}
		c.Next()
	}
}
