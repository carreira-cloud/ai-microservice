package provider

import "context"

// Message is a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`    // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// CompletionRequest is the provider-agnostic request payload.
// Stream is reserved for v2 — not exposed in MVP.
type CompletionRequest struct {
	Messages    []Message
	Model       string
	Temperature float32
	MaxTokens   int
}

// CompletionResponse is the provider-agnostic response.
type CompletionResponse struct {
	Content      string `json:"content"`
	FinishReason string `json:"finish_reason"`
	LatencyMs    int64  `json:"latency_ms"`
}

// Provider is the interface all LLM providers must implement.
// Swapping providers (Copilot → OpenAI → Anthropic) requires only a new struct.
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Name() string
}
