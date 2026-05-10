package metrics

import "github.com/prometheus/client_golang/prometheus/promauto"
import "github.com/prometheus/client_golang/prometheus"

// All metrics omit "tenant" label to avoid high cardinality.
// Per-tenant detail is available via the ai_audit_log table.
var (
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ai_requests_total",
		Help: "Total LLM completion requests.",
	}, []string{"provider", "model", "cache_hit", "error"})

	LatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ai_latency_seconds",
		Help:    "LLM request latency in seconds.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10},
	}, []string{"provider", "model", "cache_hit"})

	TokenEstimateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ai_token_estimate_total",
		Help: "Estimated prompt tokens (len(prompt_chars)/4).",
	}, []string{"provider", "model"})
)

// Record instruments a completed call.
func Record(provider, model string, latencyMs int64, cacheHit, hasError bool, promptChars int) {
	cacheHitStr := boolStr(cacheHit)
	errorStr := boolStr(hasError)

	RequestsTotal.WithLabelValues(provider, model, cacheHitStr, errorStr).Inc()
	LatencySeconds.WithLabelValues(provider, model, cacheHitStr).Observe(float64(latencyMs) / 1000)
	if promptChars > 0 {
		TokenEstimateTotal.WithLabelValues(provider, model).Add(float64(promptChars) / 4)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
