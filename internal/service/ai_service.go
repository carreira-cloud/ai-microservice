package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/audit"
	"github.com/carreira-cloud/ai-microservice/internal/cache"
	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/carreira-cloud/ai-microservice/internal/repository"
	"github.com/sirupsen/logrus"
)

// CompletionRequest is the service-level request (superset of provider.CompletionRequest).
type CompletionRequest struct {
	PromptID       string
	Variables      map[string]string
	Messages       []provider.Message
	Model          string
	Temperature    float32
	MaxTokens      int
	IdempotencyKey string
}

// CompletionResult wraps the provider response with cache/idempotency metadata.
type CompletionResult struct {
	Response   *provider.CompletionResponse
	Cached     bool
	Idempotent bool
}

// AIService orchestrates LLM calls: prompt resolution → idempotency → cache → provider → audit.
type AIService struct {
	provider      provider.Provider
	promptRepo    *repository.PromptRepository
	responseCache *cache.ResponseCache
	idemCache     *cache.IdempotencyCache
	auditWorker   *audit.Worker
	defaultTTL    time.Duration
}

func NewAIService(
	p provider.Provider,
	promptRepo *repository.PromptRepository,
	responseCache *cache.ResponseCache,
	idemCache *cache.IdempotencyCache,
	auditWorker *audit.Worker,
	cacheTTLSeconds int,
) *AIService {
	return &AIService{
		provider:      p,
		promptRepo:    promptRepo,
		responseCache: responseCache,
		idemCache:     idemCache,
		auditWorker:   auditWorker,
		defaultTTL:    time.Duration(cacheTTLSeconds) * time.Second,
	}
}

// Complete performs a chat completion with caching, idempotency, and audit.
func (s *AIService) Complete(ctx context.Context, tenantID string, req CompletionRequest) (*CompletionResult, error) {
	started := time.Now()

	// 1. Idempotency check.
	if req.IdempotencyKey != "" {
		if prev, ok := s.idemCache.Get(ctx, tenantID, req.IdempotencyKey); ok {
			logrus.WithFields(logrus.Fields{"tenant_id": tenantID, "idempotency_key": req.IdempotencyKey}).
				Info("ai_service: idempotent response served")
			s.enqueueAudit(audit.Entry{
				TenantID:   tenantID,
				TemplateID: req.PromptID,
				Provider:   s.provider.Name(),
				Model:      resolvedModel(req),
				PromptHash: hashMessages(req.Messages),
				LatencyMs:  time.Since(started).Milliseconds(),
				Idempotent: true,
			})
			return &CompletionResult{Response: prev, Idempotent: true}, nil
		}
	}

	// 2. Resolve prompt template (prepend system message).
	messages := req.Messages
	var cacheTTL time.Duration
	if req.PromptID != "" {
		sys, ttl, err := s.resolveTemplate(ctx, tenantID, req.PromptID, req.Variables)
		if err != nil {
			return nil, fmt.Errorf("ai_service: resolve template: %w", err)
		}
		messages = append(sys, messages...)
		cacheTTL = ttl
	}
	if len(messages) == 0 {
		return nil, errors.New("ai_service: messages or prompt_id required")
	}

	// 3. Response cache (key includes tenantID for isolation).
	cacheKey := buildCacheKey(tenantID, s.provider.Name(), resolvedModel(req), messages)
	if cached, ok := s.responseCache.Get(ctx, cacheKey); ok {
		logrus.WithFields(logrus.Fields{
			"tenant_id": tenantID,
			"cache_key": cacheKey[len("ai:resp:") : len("ai:resp:")+12],
		}).Info("ai_service: cache hit")
		s.enqueueAudit(audit.Entry{
			TenantID:     tenantID,
			TemplateID:   req.PromptID,
			Provider:     s.provider.Name(),
			Model:        resolvedModel(req),
			PromptHash:   hashMessages(messages),
			ResponseHash: hashString(cached.Content),
			LatencyMs:    time.Since(started).Milliseconds(),
			Cached:       true,
		})
		return &CompletionResult{Response: cached, Cached: true}, nil
	}

	// 4. Call provider.
	resp, err := s.provider.Complete(ctx, provider.CompletionRequest{
		Messages:    messages,
		Model:       resolvedModel(req),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	s.enqueueAudit(audit.Entry{
		TenantID:   tenantID,
		TemplateID: req.PromptID,
		Provider:   s.provider.Name(),
		Model:      resolvedModel(req),
		PromptHash: hashMessages(messages),
		ResponseHash: func() string {
			if resp != nil {
				return hashString(resp.Content)
			}
			return ""
		}(),
		LatencyMs: time.Since(started).Milliseconds(),
		Error:     errStr,
	})

	if err != nil {
		return nil, fmt.Errorf("ai_service: provider: %w", err)
	}

	// 5. Store in caches.
	ttl := cacheTTL
	if ttl <= 0 {
		ttl = s.defaultTTL
	}
	s.responseCache.Set(ctx, cacheKey, resp, ttl)
	if req.IdempotencyKey != "" {
		s.idemCache.Set(ctx, tenantID, req.IdempotencyKey, resp)
	}

	logrus.WithFields(logrus.Fields{
		"tenant_id":      tenantID,
		"prompt_id":      req.PromptID,
		"provider":       s.provider.Name(),
		"model":          resolvedModel(req),
		"latency_ms":     resp.LatencyMs,
		"cache_hit":      false,
		"response_chars": len(resp.Content),
	}).Info("ai_service: complete")

	return &CompletionResult{Response: resp}, nil
}

func (s *AIService) resolveTemplate(ctx context.Context, tenantID, templateID string, vars map[string]string) ([]provider.Message, time.Duration, error) {
	tmpl, err := s.promptRepo.FindByID(ctx, tenantID, templateID)
	if err != nil {
		return nil, 0, err
	}
	if tmpl.ActiveVersion == nil {
		return nil, 0, errors.New("template has no active version")
	}

	// Substitute {{variables}}.
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	resolved := strings.NewReplacer(pairs...).Replace(tmpl.ActiveVersion.SystemPrompt)

	// Warn about remaining placeholders.
	if strings.Contains(resolved, "{{") {
		logrus.WithField("template_id", templateID).Warn("ai_service: unresolved variables in template")
	}

	ttl := time.Duration(tmpl.ActiveVersion.CacheTTLSec) * time.Second
	return []provider.Message{{Role: "system", Content: resolved}}, ttl, nil
}

func (s *AIService) enqueueAudit(e audit.Entry) {
	if s.auditWorker != nil {
		s.auditWorker.Enqueue(e)
	}
}

func buildCacheKey(tenantID, providerName, model string, msgs []provider.Message) string {
	h := sha256.New()
	data, _ := json.Marshal(struct {
		T, P, M string
		Msgs    []provider.Message
	}{tenantID, providerName, model, msgs})
	h.Write(data)
	return "ai:resp:" + hex.EncodeToString(h.Sum(nil))
}

func hashMessages(msgs []provider.Message) string {
	data, _ := json.Marshal(msgs)
	return hashBytes(data)
}

func hashString(s string) string { return hashBytes([]byte(s)) }

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16]) // first 16 bytes → 32 hex chars
}

func resolvedModel(req CompletionRequest) string {
	if req.Model != "" {
		return req.Model
	}
	return "gpt-4o"
}

// ProviderName exposes the active provider name for metrics/logging.
func (s *AIService) ProviderName() string { return s.provider.Name() }
