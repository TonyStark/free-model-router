package router

import (
	"errors"
	"testing"
	"time"

	"free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
)

type mockAdapter struct {
	name         string
	providerName string
	chatFn       func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error)
	models       []string
}

func (m *mockAdapter) ProviderName() string {
	if m.providerName != "" {
		return m.providerName
	}
	return "mock"
}
func (m *mockAdapter) ChatCompletion(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	if m.chatFn != nil {
		return m.chatFn(payload, model, timeout)
	}
	return map[string]any{"ok": true}, "mock-hint", nil
}
func (m *mockAdapter) ChatCompletionStream(payload map[string]any, model string, timeout time.Duration, chunkChan chan<- []byte, resultChan chan<- or.StreamResult) {
	close(chunkChan)
	resultChan <- or.StreamResult{Err: errors.New("not implemented")}
}
func (m *mockAdapter) ChatCompletionSingleKey(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
	return m.ChatCompletion(payload, model, timeout)
}
func (m *mockAdapter) ListModels() ([]string, error) {
	return m.models, nil
}
func (m *mockAdapter) IsOpenRouter() bool { return false }

func setupFailoverRouter(t *testing.T) *FailoverRouter {
	t.Helper()
	return &FailoverRouter{
		CooldownUntil: make(map[string]int64),
	}
}

func TestIsCoolingDown(t *testing.T) {
	fr := setupFailoverRouter(t)

	if fr.IsCoolingDown("model-a") {
		t.Error("expected false for fresh model")
	}

	fr.CooldownUntil["model-a"] = time.Now().Unix() + 3600
	if !fr.IsCoolingDown("model-a") {
		t.Error("expected true for cooling model")
	}
}

func TestIsCoolingDownExpired(t *testing.T) {
	fr := setupFailoverRouter(t)
	fr.CooldownUntil["model-a"] = time.Now().Unix() - 1

	if fr.IsCoolingDown("model-a") {
		t.Error("expected false for expired cooldown")
	}
}

func TestCooldownRemaining(t *testing.T) {
	fr := setupFailoverRouter(t)

	if rem := fr.CooldownRemaining("unknown"); rem != 0 {
		t.Errorf("expected 0 for unknown model, got %v", rem)
	}

	fr.CooldownUntil["model-a"] = time.Now().Unix() + 60
	rem := fr.CooldownRemaining("model-a")
	if rem <= 0 || rem > 61*time.Second {
		t.Errorf("expected ~60s, got %v", rem)
	}
}

func TestCooldownRemainingExpired(t *testing.T) {
	fr := setupFailoverRouter(t)
	fr.CooldownUntil["model-a"] = time.Now().Unix() - 10

	if rem := fr.CooldownRemaining("model-a"); rem != 0 {
		t.Errorf("expected 0 for expired cooldown, got %v", rem)
	}
}

func TestMarkCooldown(t *testing.T) {
	fr := setupFailoverRouter(t)
	fr.MarkCooldown("req-1", "model-b", 120)

	if !fr.IsCoolingDown("model-b") {
		t.Error("expected model-b to be on cooldown after MarkCooldown")
	}
}

func TestHandleProviderErrorNonProviderError(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	// generic error — should record failure, not cooldown
	fr.HandleProviderError("req-1", "model-x", "hint1", errors.New("generic error"))
	if fr.IsCoolingDown("model-x") {
		t.Error("generic error should NOT trigger cooldown")
	}
}

func TestHandleProviderErrorNotFound(t *testing.T) {
	fr := setupFailoverRouter(t)
	fr.NotFoundCooldownSeconds = 600
	metrics.Default = metrics.New()

	fr.HandleProviderError("req-1", "model-y", "hint1", &or.ProviderError{Type: "NotFoundError", Message: "model not found"})
	if !fr.IsCoolingDown("model-y") {
		t.Error("NotFoundError should trigger cooldown")
	}
}

func TestHandleProviderErrorAuthError(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	fr.HandleProviderError("req-1", "model-z", "hint1", &or.ProviderError{Type: "AuthError", Message: "bad key"})
	if fr.IsCoolingDown("model-z") {
		t.Error("AuthError should NOT trigger cooldown")
	}
}

func TestRankedModels(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()
	metrics.Default.ModelStats = map[string]*metrics.ModelStats{
		"fast-model":  {Successes: 100, Failures: 1, TotalLatMs: 50000},   // high rate, low latency
		"slow-model":  {Successes: 50, Failures: 50, TotalLatMs: 400000},  // low rate, high latency
		"new-model":   {Successes: 0, Failures: 0},                        // no history
	}

	adapter := &mockAdapter{name: "mock"}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	modelsByProvider := map[string][]string{
		"mock": {"fast-model", "slow-model", "new-model"},
	}

	ranked := fr.RankedModels(modelsByProvider)
	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked models, got %d", len(ranked))
	}
	if ranked[0].Model != "fast-model" {
		t.Errorf("expected fast-model first, got %s", ranked[0].Model)
	}
}

func TestExecuteNonStreamSuccess(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	adapter := &mockAdapter{
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			return map[string]any{"response": "ok"}, "hint1", nil
		},
	}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	modelsByProvider := map[string][]string{"mock": {"test-model"}}
	res, model, hint, err := fr.ExecuteNonStream("req-1", map[string]any{}, modelsByProvider)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "test-model" {
		t.Errorf("expected test-model, got %q", model)
	}
	if hint != "hint1" {
		t.Errorf("expected hint1, got %q", hint)
	}
	if res["response"] != "ok" {
		t.Errorf("expected response=ok, got %v", res["response"])
	}
}

func TestExecuteNonStreamFailover(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	callCount := 0
	adapter := &mockAdapter{
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			callCount++
			if callCount == 1 {
				return nil, "hint1", &or.ProviderError{Type: "RateLimitError", Message: "too fast"}
			}
			return map[string]any{"response": "ok"}, "hint2", nil
		},
	}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	modelsByProvider := map[string][]string{"mock": {"model-a", "model-b"}}
	_, model, hint, err := fr.ExecuteNonStream("req-1", map[string]any{}, modelsByProvider)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "model-b" {
		t.Errorf("expected model-b (failover), got %q", model)
	}
	if hint != "hint2" {
		t.Errorf("expected hint2, got %q", hint)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestExecuteNonStreamAllFail(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	adapter := &mockAdapter{
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			return nil, "hint1", errors.New("all fail")
		},
	}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	modelsByProvider := map[string][]string{"mock": {"model-a", "model-b"}}
	_, _, _, err := fr.ExecuteNonStream("req-1", map[string]any{}, modelsByProvider)
	if err == nil {
		t.Fatal("expected error when all models fail")
	}
}

func TestExecuteNonStreamSkipsCooledModels(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	adapter := &mockAdapter{
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			return map[string]any{"response": "ok"}, "hint1", nil
		},
	}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	// model-a is on cooldown
	fr.CooldownUntil["model-a"] = time.Now().Unix() + 3600

	modelsByProvider := map[string][]string{"mock": {"model-a", "model-b"}}
	_, model, _, err := fr.ExecuteNonStream("req-1", map[string]any{}, modelsByProvider)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "model-b" {
		t.Errorf("expected model-b (skip cooled model-a), got %q", model)
	}
}

func TestExecuteNonStreamBudgedExhausted(t *testing.T) {
	fr := setupFailoverRouter(t)
	metrics.Default = metrics.New()

	adapter := &mockAdapter{
		chatFn: func(payload map[string]any, model string, timeout time.Duration) (map[string]any, string, error) {
			return nil, "hint1", errors.New("fail")
		},
	}
	fr.CloudAdapters = []or.LLMAdapter{adapter}

	// 3 models but MaxRetriesPerRequest defaults to 3, so all 3 should be tried
	modelsByProvider := map[string][]string{"mock": {"model-a", "model-b", "model-c"}}
	_, _, _, err := fr.ExecuteNonStream("req-1", map[string]any{}, modelsByProvider)
	if err == nil {
		t.Fatal("expected error when budget exhausted")
	}
}
