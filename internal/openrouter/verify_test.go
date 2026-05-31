package openrouter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newVerifyAdapter(srv *httptest.Server) *OpenRouterAdapter {
	return &OpenRouterAdapter{
		KeyPool:     NewKeyPool([]string{"sk-or-v1-verify-key"}),
		BaseURL:     srv.URL,
		ModelRouter: &ModelRouter{BaseURL: srv.URL},
	}
}

func TestVerifyToolSupportWithTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"The weather is sunny.","tool_calls":[{"function":{"name":"get_weather"}}]}}]}`)
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx := context.Background()
	result := VerifyToolSupport(ctx, adapter, "tool-model:free", 10)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !*result {
		t.Error("expected supported=true (tool_calls present)")
	}
}

func TestVerifyToolSupportWithoutTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"I cannot use tools."}}]}`)
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx := context.Background()
	result := VerifyToolSupport(ctx, adapter, "no-tool-model:free", 10)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if *result {
		t.Error("expected supported=false (no tool_calls)")
	}
}

func TestVerifyToolSupportNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx := context.Background()
	result := VerifyToolSupport(ctx, adapter, "not-found-model:free", 10)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if *result {
		t.Error("expected supported=false for 404 model")
	}
}

func TestVerifyToolSupportAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx := context.Background()
	result := VerifyToolSupport(ctx, adapter, "auth-model:free", 10)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if *result {
		t.Error("expected supported=false for 401 model")
	}
}

func TestVerifyToolSupportContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // will be cancelled before response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := VerifyToolSupport(ctx, adapter, "cancel-model:free", 10)
	if result != nil {
		t.Error("expected nil result when context is cancelled")
	}
}

func TestVerifyToolSupportAPIServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	adapter := newVerifyAdapter(srv)
	ctx := context.Background()
	result := VerifyToolSupport(ctx, adapter, "500-model:free", 10)

	if result != nil {
		t.Error("expected nil result for non-handled server error")
	}
}
