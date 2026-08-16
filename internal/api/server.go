package api

import (
	"net/http"

	"free-model-router/internal/config"
	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
	"free-model-router/internal/router"
)

// New creates and returns the fully-wired http.Handler.
func New(appMetrics *met.Metrics, failover *router.FailoverRouter, toolReg *or.ToolSupportRegistry, getAdaptersFn func() []or.LLMAdapter, sharedHTTPClient *http.Client) http.Handler {
	AppMetrics = appMetrics
	AppFailover = failover
	ToolRegistry = toolReg
	GetAdapters = getAdaptersFn
	SharedHTTPClient = sharedHTTPClient

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/metrics", handleMetrics)
	mux.HandleFunc("/cooldowns", handleCooldowns)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)

	cfg := config.Get()
	sem := make(chan struct{}, cfg.Global.MaxConcurrentRequests)
	return rateLimitMiddleware(sem)(corsMiddleware(loggingMiddleware(mux)))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
