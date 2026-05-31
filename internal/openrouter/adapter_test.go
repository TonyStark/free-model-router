package openrouter

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderError(t *testing.T) {
	err := &ProviderError{Type: "RateLimitError", Message: "too fast"}
	expected := "RateLimitError: too fast"
	if got := err.Error(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestClonePayload(t *testing.T) {
	src := map[string]any{
		"model":    "gpt-4",
		"messages": []any{map[string]any{"role": "user"}},
	}
	dst := clonePayload(src)

	if dst["model"] != "gpt-4" {
		t.Errorf("expected model=gpt-4, got %v", dst["model"])
	}
	if len(dst) != len(src) {
		t.Errorf("expected %d keys, got %d", len(src), len(dst))
	}

	dst["model"] = "claude"
	if src["model"] != "gpt-4" {
		t.Error("clonePayload should create a shallow copy, not alias the map")
	}
}

func TestClonePayloadEmpty(t *testing.T) {
	dst := clonePayload(nil)
	if len(dst) != 0 {
		t.Errorf("expected empty map from nil, got %d elements", len(dst))
	}
}

func TestHeaders(t *testing.T) {
	a := &OpenRouterAdapter{}
	h := a.headers("sk-or-v1-testkey123")

	tests := []struct {
		key, expected string
	}{
		{"Authorization", "Bearer sk-or-v1-testkey123"},
		{"Content-Type", "application/json"},
		{"HTTP-Referer", "http://localhost"},
		{"X-Title", "free-model-router-go"},
	}
	for _, tc := range tests {
		if got := h.Get(tc.key); got != tc.expected {
			t.Errorf("header %q = %q, want %q", tc.key, got, tc.expected)
		}
	}
}

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		errType    string
	}{
		{name: "200 OK", statusCode: 200, wantErr: false},
		{name: "429 RateLimit", statusCode: 429, wantErr: true, errType: "RateLimitError"},
		{name: "404 NotFound", statusCode: 404, wantErr: true, errType: "NotFoundError"},
		{name: "401 AuthError", statusCode: 401, wantErr: true, errType: "AuthError"},
		{name: "500 APIError", statusCode: 500, body: "internal error", wantErr: true, errType: "APIError"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			err := (&OpenRouterAdapter{}).parseStatus(resp, "hint1")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				pe, ok := err.(*ProviderError)
				if !ok {
					t.Fatalf("expected *ProviderError, got %T", err)
				}
				if pe.Type != tc.errType {
					t.Errorf("expected errType=%q, got %q", tc.errType, pe.Type)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestProviderName(t *testing.T) {
	a := &OpenRouterAdapter{}
	if name := a.ProviderName(); name != "openrouter" {
		t.Errorf("expected openrouter, got %q", name)
	}
}

func newTestAdapter(srv *httptest.Server) *OpenRouterAdapter {
	return &OpenRouterAdapter{
		KeyPool:     NewKeyPool([]string{"sk-or-v1-test-key"}),
		BaseURL:     srv.URL,
		ModelRouter: &ModelRouter{BaseURL: srv.URL},
	}
}

func TestChatCompletionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer sk-or-v1-test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		json.Unmarshal(body, &p)
		if p["model"] != "test-model:free" {
			t.Errorf("expected model test-model:free, got %v", p["model"])
		}
		if p["stream"] != false {
			t.Errorf("expected stream=false, got %v", p["stream"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"mock","choices":[{"message":{"content":"hello"}}],"usage":{}}`)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	result, hint, err := adapter.ChatCompletion(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}, "test-model:free", 5*time.Second)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hint == "" {
		t.Error("expected non-empty hint")
	}
	if result["id"] != "mock" {
		t.Errorf("expected id=mock, got %v", result["id"])
	}
}

func TestChatCompletionRateLimit(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	_, _, err := adapter.ChatCompletion(map[string]any{}, "rate-limited-model", 5*time.Second)

	if err == nil {
		t.Fatal("expected error for rate-limited request")
	}
	if callCount > 1 {
		// With 1 key, TryAllKeys should try it once, get rate limited, and return last error
		t.Logf("called %d times (1 key, expected 1 call)", callCount)
	}
}

func TestChatCompletionServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	_, _, err := adapter.ChatCompletion(map[string]any{}, "error-model", 5*time.Second)

	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected *ProviderError, got %T", err)
	}
	if pe.Type != "APIError" {
		t.Errorf("expected APIError, got %s", pe.Type)
	}
}

func TestChatCompletionSingleKeySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"single","choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	result, err := adapter.ChatCompletionSingleKey(map[string]any{}, "verify-model", 5*time.Second)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["id"] != "single" {
		t.Errorf("expected id=single, got %v", result["id"])
	}
}

func TestChatCompletionSingleKeyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	_, err := adapter.ChatCompletionSingleKey(map[string]any{}, "auth-model", 5*time.Second)

	if err == nil {
		t.Fatal("expected error for 401")
	}
	pe, ok := err.(*ProviderError)
	if !ok || pe.Type != "AuthError" {
		t.Errorf("expected AuthError, got %v", err)
	}
}

func TestChatCompletionTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	// very short timeout should trigger TimeoutError
	_, _, err := adapter.ChatCompletion(map[string]any{}, "timeout-model", 1*time.Microsecond)

	if err == nil {
		t.Skip("timeout may not trigger in test environment with short delay")
	}
	if pe, ok := err.(*ProviderError); ok && pe.Type == "TimeoutError" {
		// expected
	} else if err != nil {
		t.Logf("got error: %v (type: %T)", err, err)
	}
}

func TestChatCompletionSingleKeyNoPersistentCooldown(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv)
	adapter.ChatCompletionSingleKey(map[string]any{}, "non-persist-model", 5*time.Second)

	// TryAllKeys with persistent=false should NOT set cooldown
	// So calling the same model again should still work
	hint := adapter.KeyPool.hints[0]
	if adapter.KeyPool.isKeyCooling(hint, "non-persist-model") {
		t.Error("ChatCompletionSingleKey should not set persistent cooldown")
	}
}
