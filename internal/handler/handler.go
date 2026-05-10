package handler

import (
	"net/http"

	"github.com/carreira-cloud/ai-microservice/internal/cache"
	"github.com/carreira-cloud/ai-microservice/internal/config"
	"github.com/carreira-cloud/ai-microservice/internal/metrics"
	"github.com/carreira-cloud/ai-microservice/internal/middleware"
	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/carreira-cloud/ai-microservice/internal/repository"
	"github.com/carreira-cloud/ai-microservice/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// keep import used
var _ = cache.NewRedisClient

// RegisterRoutes wires all HTTP routes onto the engine.
func RegisterRoutes(
	r *gin.Engine,
	aiSvc *service.AIService,
	promptRepo *repository.PromptRepository,
	limiter *middleware.RateLimiter,
	db *gorm.DB,
	rdb *redis.Client,
	cfg *config.Config,
) {
	// Health & Metrics — no auth
	r.GET("/health", healthHandler(cfg.Version))
	r.GET("/ready", readyHandler(db, rdb))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Public API — X-Tenant-ID required (JWT validated upstream by api-gw)
	api := r.Group("/api/v1", middleware.TenantAuth(), middleware.RateLimit(limiter))
	api.POST("/complete", completeHandler(aiSvc))

	// Admin API — X-Gateway-Secret required
	admin := r.Group("/admin", middleware.GatewayAuth(cfg.GatewaySecret))
	admin.GET("/prompts", listPromptsHandler(promptRepo))
	admin.POST("/prompts", createPromptHandler(promptRepo))
	admin.GET("/prompts/:id", getPromptHandler(promptRepo))
	admin.PUT("/prompts/:id", updatePromptHandler(promptRepo))
	admin.GET("/prompts/:id/versions", listVersionsHandler(promptRepo))
}

// ── /health (liveness) ────────────────────────────────────────────────────────

func healthHandler(version string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": version})
	}
}

// ── /ready (readiness) ────────────────────────────────────────────────────────

func readyHandler(db *gorm.DB, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		httpStatus := http.StatusOK
		checks := gin.H{}

		sqlDB, err := db.DB()
		if err != nil || sqlDB.Ping() != nil {
			checks["db"] = "error"
			httpStatus = http.StatusServiceUnavailable
		} else {
			checks["db"] = "ok"
		}
		if rdb != nil {
			if rdb.Ping(c.Request.Context()).Err() != nil {
				checks["redis"] = "error"
			} else {
				checks["redis"] = "ok"
			}
		}
		checks["status"] = map[bool]string{true: "ok", false: "not_ready"}[httpStatus == http.StatusOK]
		c.JSON(httpStatus, checks)
	}
}

// ── POST /api/v1/complete ─────────────────────────────────────────────────────

type msgDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completeRequest struct {
	PromptID       string            `json:"prompt_id"`
	Variables      map[string]string `json:"variables"`
	Messages       []msgDTO          `json:"messages"`
	Model          string            `json:"model"`
	Temperature    float32           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens"`
	IdempotencyKey string            `json:"idempotency_key"`
	Stream         bool              `json:"stream"`
}

func completeHandler(svc *service.AIService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req completeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Streaming out-of-scope for MVP — reject explicitly.
		if req.Stream {
			c.JSON(http.StatusNotImplemented, gin.H{
				"error": "streaming_not_supported",
				"info":  "SSE streaming is available in v2",
			})
			return
		}

		if req.PromptID == "" && len(req.Messages) == 0 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "messages_or_prompt_id_required",
			})
			return
		}

		tenantID := c.GetString("tenant_id")

		svcReq := service.CompletionRequest{
			PromptID:       req.PromptID,
			Variables:      req.Variables,
			Model:          req.Model,
			Temperature:    req.Temperature,
			MaxTokens:      req.MaxTokens,
			IdempotencyKey: req.IdempotencyKey,
		}
		var promptChars int
		for _, m := range req.Messages {
			svcReq.Messages = append(svcReq.Messages, provider.Message{Role: m.Role, Content: m.Content})
			promptChars += len(m.Content)
		}

		result, err := svc.Complete(c.Request.Context(), tenantID, svcReq)
		if err != nil {
			metrics.Record(svc.ProviderName(), req.Model, 0, false, true, promptChars)
			c.JSON(http.StatusBadGateway, gin.H{"error": "provider_unavailable"})
			return
		}

		cacheHit := result.Cached || result.Idempotent
		c.Header("X-Cache", map[bool]string{true: "HIT", false: "MISS"}[cacheHit])
		metrics.Record(svc.ProviderName(), req.Model, result.Response.LatencyMs, cacheHit, false, promptChars)

		c.JSON(http.StatusOK, gin.H{
			"content":       result.Response.Content,
			"finish_reason": result.Response.FinishReason,
			"cached":        result.Cached,
			"idempotent":    result.Idempotent,
			"latency_ms":    result.Response.LatencyMs,
		})
	}
}

// ── Admin prompt handlers ─────────────────────────────────────────────────────

type createPromptReq struct {
	TenantID     string  `json:"tenant_id" binding:"required"`
	Name         string  `json:"name" binding:"required"`
	Description  string  `json:"description"`
	SystemPrompt string  `json:"system_prompt" binding:"required"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Temperature  float32 `json:"temperature"`
	MaxTokens    int     `json:"max_tokens"`
	CacheTTLSec  int     `json:"cache_ttl_seconds"`
}

func listPromptsHandler(repo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.Query("tenant_id")
		if tenantID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id query param required"})
			return
		}
		list, err := repo.List(c.Request.Context(), tenantID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"templates": list, "count": len(list)})
	}
}

func createPromptHandler(repo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPromptReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		tmpl, err := repo.Create(c.Request.Context(), repository.CreateTemplateInput{
			TenantID: req.TenantID, Name: req.Name, Description: req.Description,
			SystemPrompt: req.SystemPrompt, Provider: req.Provider, Model: req.Model,
			Temperature: req.Temperature, MaxTokens: req.MaxTokens, CacheTTLSec: req.CacheTTLSec,
		})
		if err == repository.ErrConflict {
			c.JSON(http.StatusConflict, gin.H{"error": "name_conflict"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			return
		}
		c.JSON(http.StatusCreated, tmpl)
	}
}

func getPromptHandler(repo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.Query("tenant_id")
		if tenantID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id query param required"})
			return
		}
		tmpl, err := repo.FindByID(c.Request.Context(), tenantID, c.Param("id"))
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			return
		}
		c.JSON(http.StatusOK, tmpl)
	}
}

func updatePromptHandler(repo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPromptReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		tenantID := req.TenantID
		if tenantID == "" {
			tenantID = c.Query("tenant_id")
		}
		v, err := repo.CreateVersion(c.Request.Context(), tenantID, c.Param("id"), repository.CreateTemplateInput{
			TenantID: tenantID, SystemPrompt: req.SystemPrompt,
			Provider: req.Provider, Model: req.Model,
			Temperature: req.Temperature, MaxTokens: req.MaxTokens, CacheTTLSec: req.CacheTTLSec,
		})
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			return
		}
		c.JSON(http.StatusOK, v)
	}
}

func listVersionsHandler(repo *repository.PromptRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.Query("tenant_id")
		if tenantID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id query param required"})
			return
		}
		versions, err := repo.ListVersions(c.Request.Context(), tenantID, c.Param("id"))
		if err == repository.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"versions": versions})
	}
}
