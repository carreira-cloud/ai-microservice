package copilot_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/provider"
	"github.com/carreira-cloud/ai-microservice/internal/provider/copilot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeStub(t *testing.T, tokenCalls, completionCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/copilot_internal/v2/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"token":      "test-copilot-token",
			"expires_at": time.Now().Add(30 * time.Minute).Unix(),
		})
	})

	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		completionCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "test response"}, "finish_reason": "stop"},
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestCopilot_TokenCachedAcrossRequests(t *testing.T) {
	var tokenCalls, completionCalls atomic.Int32
	srv := makeStub(t, &tokenCalls, &completionCalls)
	defer srv.Close()

	client := &http.Client{
		Transport: rewriteTransport(srv.URL),
	}
	p := copilot.New("oauth-token", client)

	req := provider.CompletionRequest{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	_, err := p.Complete(context.Background(), req)
	require.NoError(t, err)
	_, err = p.Complete(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, int32(1), tokenCalls.Load(), "token exchange should be called only once")
	assert.Equal(t, int32(2), completionCalls.Load())
}

func TestCopilot_NetworkRetry(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/copilot_internal/v2/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"token": "tok", "expires_at": time.Now().Add(30 * time.Minute).Unix(),
		})
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			// first attempt: abrupt close
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
			http.Error(w, "fail", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "retried"}, "finish_reason": "stop"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := copilot.New("tok", &http.Client{Transport: rewriteTransport(srv.URL)})
	resp, err := p.Complete(context.Background(), provider.CompletionRequest{
		Messages: []provider.Message{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "retried", resp.Content)
	assert.GreaterOrEqual(t, attempts.Load(), int32(2))
}

func TestCopilot_BothAttemptsFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/copilot_internal/v2/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token": "tok", "expires_at": time.Now().Add(30 * time.Minute).Unix()})
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := copilot.New("tok", &http.Client{Transport: rewriteTransport(srv.URL)})
	_, err := p.Complete(context.Background(), provider.CompletionRequest{
		Messages: []provider.Message{{Role: "user", Content: "hello"}},
	})
	require.Error(t, err)
}

// rewriteTransport rewrites request URLs to point to the stub server.
type rewriteTransport string

func (base rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Host = string(base)[len("http://"):]
	req.URL.Scheme = "http"
	return http.DefaultTransport.RoundTrip(req)
}
