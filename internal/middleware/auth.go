package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// TenantAuth validates the X-Tenant-ID header.
// Injects tenant_id into gin.Context for downstream handlers.
func TenantAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tid := strings.TrimSpace(c.GetHeader("X-Tenant-ID"))
		if tid == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing_tenant_id"})
			return
		}
		c.Set("tenant_id", tid)
		c.Next()
	}
}

// GatewayAuth validates X-Gateway-Secret using HMAC comparison to avoid
// timing leaks from raw byte length differences.
func GatewayAuth(secret string) gin.HandlerFunc {
	expected := hmacDigest(secret)
	return func(c *gin.Context) {
		got := hmacDigest(c.GetHeader("X-Gateway-Secret"))
		if !hmac.Equal(expected, got) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func hmacDigest(s string) []byte {
	h := hmac.New(sha256.New, []byte("ai-gateway-v1"))
	h.Write([]byte(s))
	return h.Sum(nil)
}
