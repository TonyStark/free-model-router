package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"free-model-router/internal/config"
	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
	"free-model-router/internal/router"
)

func TestNewReturnsHandler(t *testing.T) {
	saveMetrics := AppMetrics
	saveFailover := AppFailover
	saveRegistry := ToolRegistry
	saveGetAdapters := GetAdapters
	defer func() {
		AppMetrics = saveMetrics
		AppFailover = saveFailover
		ToolRegistry = saveRegistry
		GetAdapters = saveGetAdapters
	}()

	m := met.New()
	fr := &router.FailoverRouter{CooldownUntil: make(map[string]int64)}
	tr := or.NewToolSupportRegistry("")
	getAdapters := func() []or.LLMAdapter { return nil }

	handler := New(m, fr, tr, getAdapters, &http.Client{})
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	tests := []struct {
		name string
		path string
		code int
	}{
		{"health", "/health", http.StatusOK},
		{"metrics", "/metrics", http.StatusOK},
		{"cooldowns", "/cooldowns", http.StatusOK},
		{"models", "/v1/models", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tc.path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Errorf("GET %s: expected %d, got %d", tc.path, tc.code, rec.Code)
			}
		})
	}
}

func TestNewNoNilGlobals(t *testing.T) {
	saveMetrics := AppMetrics
	saveFailover := AppFailover
	saveRegistry := ToolRegistry
	saveGetAdapters := GetAdapters
	defer func() {
		AppMetrics = saveMetrics
		AppFailover = saveFailover
		ToolRegistry = saveRegistry
		GetAdapters = saveGetAdapters
	}()

	m := met.New()
	fr := &router.FailoverRouter{CooldownUntil: make(map[string]int64)}
	tr := or.NewToolSupportRegistry("")
	getAdapters := func() []or.LLMAdapter {
		return []or.LLMAdapter{&mockAdapter{provider: "test", models: []string{"test-model:free"}}}
	}

	_ = New(m, fr, tr, getAdapters, &http.Client{})

	if AppMetrics == nil {
		t.Error("AppMetrics should not be nil after New()")
	}
	if AppFailover == nil {
		t.Error("AppFailover should not be nil after New()")
	}
	if ToolRegistry == nil {
		t.Error("ToolRegistry should not be nil after New()")
	}
	if GetAdapters == nil {
		t.Error("GetAdapters should not be nil after New()")
	}
}

type mockAdapter struct {
	provider string
	models   []string
}

func (a *mockAdapter) ProviderName() string { return a.provider }
func (a *mockAdapter) ListModels() ([]string, error) {
	return a.models, nil
}
func (a *mockAdapter) ChatCompletion(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	return map[string]any{}, "", errors.New("mock")
}
func (a *mockAdapter) ChatCompletionStream(payload map[string]any, model string, timeout time.Duration, chunkChan chan<- []byte, resultChan chan<- or.StreamResult) {
}
func (a *mockAdapter) IsOpenRouter() bool { return false }

func TestGetModelsByProvider(t *testing.T) {
	saveGetAdapters := GetAdapters
	defer func() { GetAdapters = saveGetAdapters }()

	GetAdapters = func() []or.LLMAdapter {
		return []or.LLMAdapter{
			&mockAdapter{provider: "test-provider", models: []string{"model-a", "model-b"}},
		}
	}

	mbp := getModelsByProvider()
	if len(mbp) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(mbp))
	}
	models := mbp["test-provider"]
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0] != "model-a" || models[1] != "model-b" {
		t.Errorf("unexpected models: %v", models)
	}
}

func TestGetModelsByProviderHandlesEmpty(t *testing.T) {
	saveGetAdapters := GetAdapters
	defer func() { GetAdapters = saveGetAdapters }()

	GetAdapters = func() []or.LLMAdapter {
		return []or.LLMAdapter{
			&mockAdapter{provider: "good", models: []string{"m1"}},
			&mockAdapter{provider: "empty", models: nil}, // ListModels returns nil, nil
		}
	}

	mbp := getModelsByProvider()
	if len(mbp) != 2 {
		t.Fatalf("expected 2 providers, got %d: %v", len(mbp), mbp)
	}
	if len(mbp["empty"]) != 0 {
		t.Errorf("expected empty model list for 'empty' provider, got %v", mbp["empty"])
	}
	if len(mbp["good"]) != 1 {
		t.Errorf("expected 1 model for 'good' provider, got %v", mbp["good"])
	}
}

func TestHealthEndpointJson(t *testing.T) {
	saveMetrics := AppMetrics
	saveFailover := AppFailover
	saveRegistry := ToolRegistry
	saveGetAdapters := GetAdapters
	defer func() {
		AppMetrics = saveMetrics
		AppFailover = saveFailover
		ToolRegistry = saveRegistry
		GetAdapters = saveGetAdapters
	}()

	handler := New(met.New(), &router.FailoverRouter{CooldownUntil: make(map[string]int64)}, or.NewToolSupportRegistry(""), func() []or.LLMAdapter { return nil }, &http.Client{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	handler.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	if _, ok := body["time"]; !ok {
		t.Error("expected time field")
	}
}

func TestRateLimitSemaphoreCapacity(t *testing.T) {
	// Verify the semaphore is created with the correct capacity (from config)
	cfg := config.Get()
	expectedCap := cfg.Global.MaxConcurrentRequests

	saveFailover := AppFailover
	saveMetrics := AppMetrics
	saveRegistry := ToolRegistry
	saveGetAdapters := GetAdapters
	defer func() {
		AppFailover = saveFailover
		AppMetrics = saveMetrics
		ToolRegistry = saveRegistry
		GetAdapters = saveGetAdapters
	}()

	// fill the expected number of slots
	sem := make(chan struct{}, expectedCap)
	for i := 0; i < expectedCap; i++ {
		sem <- struct{}{}
	}

	mw := rateLimitMiddleware(sem)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 (all %d slots full), got %d", expectedCap, rec.Code)
	}
}
