package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
)

type testAdapter struct {
	provider string
	models   []string
	chatFn   func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error)
}

func (a *testAdapter) ProviderName() string { return a.provider }
func (a *testAdapter) ChatCompletion(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	if a.chatFn != nil {
		return a.chatFn(payload, model, timeout)
	}
	return map[string]any{"ok": true}, "hint", nil
}
func (a *testAdapter) ChatCompletionStream(payload map[string]any, model string, timeout time.Duration, chunkChan chan<- []byte, resultChan chan<- or.StreamResult) {
	close(chunkChan)
	resultChan <- or.StreamResult{Err: errors.New("not implemented in test")}
}
func (a *testAdapter) ChatCompletionSingleKey(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	return a.ChatCompletion(payload, model, timeout)
}
func (a *testAdapter) ListModels() ([]string, error) { return a.models, nil }
func (a *testAdapter) IsOpenRouter() bool                              { return a.provider == "openrouter" }

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

func setupTestHandler(t *testing.T) http.Handler {
	t.Helper()
	AppMetrics = met.New()
	AppFailover.CooldownUntil = make(map[string]int64)

	adapter := &testAdapter{
		provider: "openrouter",
		models:   []string{"test-model-1", "test-model-2"},
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			return map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "hello"}}}}, "hint1", nil
		},
	}
	ToolRegistry = or.NewToolSupportRegistry("")
	GetAdapters = func() []or.LLMAdapter { return []or.LLMAdapter{adapter} }
	AppFailover.CloudAdapters = []or.LLMAdapter{adapter}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/metrics", handleMetrics)
	mux.HandleFunc("/cooldowns", handleCooldowns)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)

	sem := make(chan struct{}, 50)
	return loggingMiddleware(rateLimitMiddleware(sem)(mux))
}

func TestHandleHealth(t *testing.T) {
	handler := setupTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body["status"])
	}
	if _, ok := body["time"]; !ok {
		t.Error("expected time field in response")
	}
}

func TestHandleMetrics(t *testing.T) {
	handler := setupTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["total_requests"]; !ok {
		t.Error("expected total_requests in metrics")
	}
	if _, ok := body["models"]; !ok {
		t.Error("expected models in metrics")
	}
}

func TestHandleCooldowns(t *testing.T) {
	handler := setupTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/cooldowns", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
}

func TestHandleModels(t *testing.T) {
	handler := setupTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "list" {
		t.Errorf("expected object=list, got %s", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected at least 1 model")
	}
	// should include auto, free-model-router/auto, test-model-1, test-model-2
	if len(resp.Data) < 4 {
		t.Errorf("expected at least 4 models, got %d", len(resp.Data))
	}
}

func TestHandleChatCompletionsSpecificModel(t *testing.T) {
	handler := setupTestHandler(t)
	body := map[string]any{"model": "test-model-1", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	data, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}
}

func TestHandleChatCompletionsInvalidJSON(t *testing.T) {
	handler := setupTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte("{invalid")))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleChatCompletionsAllModelsOnCooldown(t *testing.T) {
	handler := setupTestHandler(t)
	// put models on cooldown
	AppFailover.CooldownUntil["test-model-1"] = time.Now().Unix() + 3600
	AppFailover.CooldownUntil["test-model-2"] = time.Now().Unix() + 3600

	body := map[string]any{"model": "auto", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	data, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHandleChatCompletionsAutoModel(t *testing.T) {
	handler := setupTestHandler(t)
	AppFailover.CooldownUntil["test-model-1"] = time.Now().Unix() - 3600 // expired, ok to use

	body := map[string]any{"model": "auto", "messages": []map[string]string{{"role": "user", "content": "hello"}}}
	data, _ := json.Marshal(body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatCompletionsStreamRequest(t *testing.T) {
	handler := setupTestHandler(t)
	body := map[string]any{"model": "test-model-1", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
	data, _ := json.Marshal(body)

	rec := &flushRecorder{httptest.NewRecorder()}
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	// streaming handler will try test-model-1, ChatCompletionStream returns error,
	// then since no models left and sentBytes=false, writes error SSE and returns 200
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for stream (even with error), got %d", rec.Code)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fill the slot

	mw := rateLimitMiddleware(sem)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestStatusWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: 200}

	sw.WriteHeader(201)
	if sw.status != 201 {
		t.Errorf("expected 201, got %d", sw.status)
	}
	if !sw.wrote {
		t.Error("expected wrote=true")
	}
}

func TestNewRequestID(t *testing.T) {
	requestCounter.Store(0)
	id1 := newRequestID()
	id2 := newRequestID()
	if id1 == id2 {
		t.Error("expected different request IDs")
	}
	if id1 != "req-0001" {
		t.Errorf("expected req-0001, got %s", id1)
	}
}
