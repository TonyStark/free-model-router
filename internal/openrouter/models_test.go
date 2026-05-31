package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetFreeModelsFiltersCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4o:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "claude-3:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "gpt-4o", "pricing": map[string]any{"prompt": "10", "completion": "30"}},
				{"id": "gemini-pro:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 300, ExcludeKeywords: []string{"claude"}}
	models, err := mr.GetFreeModels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"gpt-4o:free", "gemini-pro:free"}
	if len(models) != len(expected) {
		t.Fatalf("expected %d models, got %d: %v", len(expected), len(models), models)
	}
	for i := range expected {
		if models[i] != expected[i] {
			t.Errorf("index %d: expected %q, got %q", i, expected[i], models[i])
		}
	}
}

func TestGetFreeModelsNoFreeModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4o", "pricing": map[string]any{"prompt": "10", "completion": "30"}},
				{"id": "claude-3-opus", "pricing": map[string]any{"prompt": "15", "completion": "60"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 300}
	models, err := mr.GetFreeModels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 free models, got %d: %v", len(models), models)
	}
}

func TestGetFreeModelsCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := map[string]any{
			"data": []map[string]any{
				{"id": "cached-model:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 300}

	// first call — fetches
	models1, err := mr.GetFreeModels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// second call — should be cached
	models2, err := mr.GetFreeModels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 0 additional calls (cached), got total %d", callCount)
	}
	if len(models1) != len(models2) || models1[0] != models2[0] {
		t.Error("cached result should match fresh result")
	}
}

func TestGetFreeModelsCacheExpiry(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := map[string]any{
			"data": []map[string]any{
				{"id": "fresh-model:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 0} // zero TTL = expired immediately
	mr.GetFreeModels()
	mr.GetFreeModels()

	if callCount != 2 {
		t.Errorf("expected 2 calls (0 TTL, no caching), got %d", callCount)
	}
}

func TestGetFreeModelsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 300}
	models, err := mr.GetFreeModels()
	// NOTE: GetFreeModels does not check HTTP status code; it silently
	// decodes the empty body and returns zero models.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models for 500 response, got %d", len(models))
	}
}

func TestGetFreeModelsExcludeKeywords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []map[string]any{
				{"id": "dolphin-mixtral:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "llama-3:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
				{"id": "liquid-model:free", "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 300, ExcludeKeywords: []string{"dolphin", "liquid", "arcee"}}
	models, err := mr.GetFreeModels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0] != "llama-3:free" {
		t.Errorf("expected [llama-3:free], got %v", models)
	}
}

func TestGetFreeModelsRefreshCacheAfterTTL(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := map[string]any{
			"data": []map[string]any{
				{"id": fmt.Sprintf("model-v%d:free", callCount), "pricing": map[string]any{"prompt": "0", "completion": "0"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mr := &ModelRouter{BaseURL: srv.URL, CacheTTL: 1} // 1-second TTL

	models1, _ := mr.GetFreeModels()
	_ = models1

	// force cache expiry
	mr.cacheTime = time.Now().Unix() - 10

	models2, _ := mr.GetFreeModels()
	if callCount != 2 {
		t.Errorf("expected 2 calls after cache expiry, got %d", callCount)
	}
	if len(models2) == 0 {
		t.Error("expected non-empty result after refresh")
	}
}
