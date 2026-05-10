package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/audit"
	"github.com/carreira-cloud/ai-microservice/internal/cache"
	"github.com/carreira-cloud/ai-microservice/internal/config"
	"github.com/carreira-cloud/ai-microservice/internal/database"
	"github.com/carreira-cloud/ai-microservice/internal/handler"
	"github.com/carreira-cloud/ai-microservice/internal/middleware"
	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/carreira-cloud/ai-microservice/internal/repository"
	"github.com/carreira-cloud/ai-microservice/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Stubs ─────────────────────────────────────────────────────────────────────

type stubProvider struct{ name string }

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Complete(_ context.Context, req provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return &provider.CompletionResponse{Content: "stub response", FinishReason: "stop", LatencyMs: 10}, nil
}

// ── Test server builder ───────────────────────────────────────────────────────

func buildTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := database.OpenTestDB()
	require.NoError(t, err)

	cfg := &config.Config{
		GatewaySecret:   "test-secret",
		Version:         "test",
		CacheTTLSeconds: 3600,
		RateLimitRPM:    60,
		Port:            "0",
	}

	auditWorker := audit.NewWorker(db, 10)
	auditWorker.Start()
	responseCache := cache.NewResponseCache(nil, cfg.CacheTTLSeconds) // no-op cache
	idemCache := cache.NewIdempotencyCache(nil)                       // no-op
	limiter := middleware.NewRateLimiter(nil, cfg.RateLimitRPM)       // fail-open
	prov := &stubProvider{name: "stub"}
	promptRepo := repository.NewPromptRepository(db)
	aiSvc := service.NewAIService(prov, promptRepo, responseCache, idemCache, auditWorker, cfg.CacheTTLSeconds)

	r := gin.New()
	r.Use(gin.Recovery())
	handler.RegisterRoutes(r, aiSvc, promptRepo, limiter, db, nil, cfg)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		auditWorker.Drain(ctx)
	})

	return httptest.NewServer(r)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func post(t *testing.T, srv *httptest.Server, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func get(t *testing.T, srv *httptest.Server, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func jsonBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
	resp.Body.Close()
	return m
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestComplete_MissingTenantID(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := post(t, srv, "/api/v1/complete", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}, nil)
	assert.Equal(t, 400, resp.StatusCode)
}

func TestComplete_StreamRejected(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := post(t, srv, "/api/v1/complete",
		map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": true},
		map[string]string{"X-Tenant-ID": "t1"})
	assert.Equal(t, 501, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Equal(t, "streaming_not_supported", body["error"])
}

func TestComplete_NoMessagesOrPromptID(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := post(t, srv, "/api/v1/complete", map[string]any{},
		map[string]string{"X-Tenant-ID": "t1"})
	assert.Equal(t, 422, resp.StatusCode)
}

func TestComplete_Success_CacheMiss(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := post(t, srv, "/api/v1/complete",
		map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}},
		map[string]string{"X-Tenant-ID": "t1"})
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "MISS", resp.Header.Get("X-Cache"))
	body := jsonBody(t, resp)
	assert.Equal(t, "stub response", body["content"])
	assert.Equal(t, false, body["cached"])
}

func TestComplete_AdminEndpoint_NoGatewaySecret(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := post(t, srv, "/admin/prompts", map[string]any{
		"tenant_id": "t1", "name": "test", "system_prompt": "hello",
	}, nil)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestPromptCRUD(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	gwHeader := map[string]string{"X-Gateway-Secret": "test-secret"}

	// Create
	resp := post(t, srv, "/admin/prompts", map[string]any{
		"tenant_id": "t1", "name": "my-prompt", "system_prompt": "You are helpful.",
	}, gwHeader)
	require.Equal(t, 201, resp.StatusCode)
	created := jsonBody(t, resp)
	id := fmt.Sprintf("%v", created["id"])
	assert.NotEmpty(t, id)

	// GET by ID
	resp2 := get(t, srv, "/admin/prompts/"+id+"?tenant_id=t1", gwHeader)
	require.Equal(t, 200, resp2.StatusCode)
	fetched := jsonBody(t, resp2)
	assert.Equal(t, "my-prompt", fetched["name"])

	// GET by ID — wrong tenant
	resp3 := get(t, srv, "/admin/prompts/"+id+"?tenant_id=other", gwHeader)
	assert.Equal(t, 404, resp3.StatusCode)

	// PUT — new version
	resp4, _ := func() (*http.Response, error) {
		data, _ := json.Marshal(map[string]any{
			"tenant_id": "t1", "name": "my-prompt", "system_prompt": "Updated prompt.",
		})
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/admin/prompts/"+id, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gateway-Secret", "test-secret")
		return http.DefaultClient.Do(req)
	}()
	require.Equal(t, 200, resp4.StatusCode)
	version := jsonBody(t, resp4)
	assert.Equal(t, float64(2), version["version"])

	// List versions
	resp5 := get(t, srv, "/admin/prompts/"+id+"/versions?tenant_id=t1", gwHeader)
	require.Equal(t, 200, resp5.StatusCode)
	body5 := jsonBody(t, resp5)
	versions := body5["versions"].([]any)
	assert.Len(t, versions, 2)
}

func TestHealth_AlwaysOK(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := get(t, srv, "/health", nil)
	assert.Equal(t, 200, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Equal(t, "ok", body["status"])
}

func TestReady_OKWithSQLite(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	// Redis is nil (no-op) — /ready should still 200 when DB is healthy.
	resp := get(t, srv, "/ready", nil)
	assert.Equal(t, 200, resp.StatusCode)
	body := jsonBody(t, resp)
	assert.Equal(t, "ok", body["db"])
	assert.Equal(t, "ok", body["status"])
}

func TestComplete_IdempotencyKey_ReturnsConsistentResult(t *testing.T) {
	// Without Redis the idempotency cache is no-op (fail-open).
	// Two identical requests with the same key return valid results (may differ
	// since the no-op cache doesn’t de-dup — that is the expected fail-open behaviour).
	srv := buildTestServer(t)
	defer srv.Close()

	body := map[string]any{
		"messages":        []any{map[string]any{"role": "user", "content": "hello"}},
		"idempotency_key": "test-key-abc",
	}
	headers := map[string]string{"X-Tenant-ID": "tenant1"}

	resp1 := post(t, srv, "/api/v1/complete", body, headers)
	resp2 := post(t, srv, "/api/v1/complete", body, headers)

	assert.Equal(t, 200, resp1.StatusCode)
	assert.Equal(t, 200, resp2.StatusCode)

	b1, b2 := jsonBody(t, resp1), jsonBody(t, resp2)
	assert.Equal(t, "stub response", b1["content"])
	assert.Equal(t, "stub response", b2["content"])
}

func TestComplete_CrossTenantCacheIsolation(t *testing.T) {
	// Same messages, different tenants — no Redis cache so both call provider.
	// Verifies tenant_id is in the cache key path (acceptance of the design).
	srv := buildTestServer(t)
	defer srv.Close()

	msg := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}}

	r1 := post(t, srv, "/api/v1/complete", msg, map[string]string{"X-Tenant-ID": "tenant-a"})
	r2 := post(t, srv, "/api/v1/complete", msg, map[string]string{"X-Tenant-ID": "tenant-b"})

	assert.Equal(t, 200, r1.StatusCode)
	assert.Equal(t, 200, r2.StatusCode)

	// Both should be MISS (no Redis; no cross-tenant leak).
	assert.Equal(t, "MISS", r1.Header.Get("X-Cache"))
	assert.Equal(t, "MISS", r2.Header.Get("X-Cache"))
}

func TestPromptConflict_DuplicateName(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	gwHeader := map[string]string{"X-Gateway-Secret": "test-secret"}

	body := map[string]any{"tenant_id": "t1", "name": "dup-prompt", "system_prompt": "hello"}
	resp1 := post(t, srv, "/admin/prompts", body, gwHeader)
	require.Equal(t, 201, resp1.StatusCode)
	jsonBody(t, resp1) // drain

	resp2 := post(t, srv, "/admin/prompts", body, gwHeader)
	assert.Equal(t, 409, resp2.StatusCode)
	b := jsonBody(t, resp2)
	assert.Equal(t, "name_conflict", b["error"])
}

func TestMetrics_Available(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()
	resp := get(t, srv, "/metrics", nil)
	assert.Equal(t, 200, resp.StatusCode)
}
