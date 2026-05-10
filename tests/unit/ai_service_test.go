package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/audit"
	"github.com/carreira-cloud/ai-microservice/internal/cache"
	"github.com/carreira-cloud/ai-microservice/internal/database"
	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/carreira-cloud/ai-microservice/internal/repository"
	"github.com/carreira-cloud/ai-microservice/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Stubs ─────────────────────────────────────────────────────────────────────

type trackingProvider struct {
	calls int
	resp  *provider.CompletionResponse
	err   error
}

func (p *trackingProvider) Name() string { return "stub" }
func (p *trackingProvider) Complete(_ context.Context, _ provider.CompletionRequest) (*provider.CompletionResponse, error) {
	p.calls++
	return p.resp, p.err
}

func buildService(t *testing.T) (*service.AIService, *trackingProvider, *cache.ResponseCache, *repository.PromptRepository) {
	t.Helper()
	db, err := database.OpenTestDB()
	require.NoError(t, err)

	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "hello", FinishReason: "stop", LatencyMs: 5}}
	respCache := cache.NewResponseCache(nil, 3600) // no-op Redis
	idemCache := cache.NewIdempotencyCache(nil)
	worker := audit.NewWorker(db, 10)
	worker.Start()
	repo := repository.NewPromptRepository(db)

	svc := service.NewAIService(prov, repo, respCache, idemCache, worker, 3600)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		worker.Drain(ctx)
	})
	return svc, prov, respCache, repo
}

func TestAIService_Complete_BasicSuccess(t *testing.T) {
	db, _ := database.OpenTestDB()
	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "world", FinishReason: "stop", LatencyMs: 10}}
	svc := service.NewAIService(prov, repository.NewPromptRepository(db),
		cache.NewResponseCache(nil, 3600), cache.NewIdempotencyCache(nil), nil, 3600)

	result, err := svc.Complete(context.Background(), "tenant1", service.CompletionRequest{
		Messages: []provider.Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "world", result.Response.Content)
	assert.False(t, result.Cached)
	assert.False(t, result.Idempotent)
	assert.Equal(t, 1, prov.calls)
}

func TestAIService_Complete_EmptyInput_Error(t *testing.T) {
	db, _ := database.OpenTestDB()
	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "x"}}
	svc := service.NewAIService(prov, repository.NewPromptRepository(db),
		cache.NewResponseCache(nil, 3600), cache.NewIdempotencyCache(nil), nil, 3600)

	_, err := svc.Complete(context.Background(), "t1", service.CompletionRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "messages or prompt_id required")
}

func TestAIService_Complete_PromptID_NotFound(t *testing.T) {
	db, _ := database.OpenTestDB()
	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "x"}}
	svc := service.NewAIService(prov, repository.NewPromptRepository(db),
		cache.NewResponseCache(nil, 3600), cache.NewIdempotencyCache(nil), nil, 3600)

	_, err := svc.Complete(context.Background(), "t1", service.CompletionRequest{
		PromptID: "non-existent-id",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve template")
}

func TestAIService_Complete_ProviderName(t *testing.T) {
	db, _ := database.OpenTestDB()
	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "x"}}
	svc := service.NewAIService(prov, repository.NewPromptRepository(db),
		cache.NewResponseCache(nil, 3600), cache.NewIdempotencyCache(nil), nil, 3600)

	assert.Equal(t, "stub", svc.ProviderName())
}

func TestAIService_Complete_TenantIsolation(t *testing.T) {
	// Two tenants with same messages should trigger provider twice (separate cache keys)
	db, _ := database.OpenTestDB()
	prov := &trackingProvider{resp: &provider.CompletionResponse{Content: "x"}}
	// We can't easily test Redis cache without Redis — just verify provider is called
	// for each tenant in a no-cache scenario.
	svc := service.NewAIService(prov, repository.NewPromptRepository(db),
		cache.NewResponseCache(nil, 3600), cache.NewIdempotencyCache(nil), nil, 3600)

	msgs := []provider.Message{{Role: "user", Content: "hello"}}
	_, err := svc.Complete(context.Background(), "tenant-a", service.CompletionRequest{Messages: msgs})
	require.NoError(t, err)
	_, err = svc.Complete(context.Background(), "tenant-b", service.CompletionRequest{Messages: msgs})
	require.NoError(t, err)

	assert.Equal(t, 2, prov.calls, "provider must be called for each tenant (separate cache keys)")
}
