package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := defaults()
	if cfg.Global.TimeoutSeconds != 30.0 {
		t.Errorf("expected TimeoutSeconds=30, got %f", cfg.Global.TimeoutSeconds)
	}
	if cfg.Global.MaxConcurrentRequests != 50 {
		t.Errorf("expected MaxConcurrentRequests=50, got %d", cfg.Global.MaxConcurrentRequests)
	}
	if len(cfg.EnabledProviders) != 1 || cfg.EnabledProviders[0] != "openrouter" {
		t.Errorf("expected [openrouter], got %v", cfg.EnabledProviders)
	}
	if _, ok := cfg.Providers["openrouter"]; !ok {
		t.Error("expected openrouter in Providers")
	}
}

func TestLoadWithValidFile(t *testing.T) {
	tmp := t.TempDir()
	origFile := cfgFile
	cfgFile = filepath.Join(tmp, "test-config.json")
	defer func() { cfgFile = origFile }()

	content := `{"global": {"timeout_seconds": 15.0, "max_concurrent_requests": 10, "verify_tool_support": false}}`
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	Load()

	if cfg.Global.TimeoutSeconds != 15.0 {
		t.Errorf("expected TimeoutSeconds=15.0, got %f", cfg.Global.TimeoutSeconds)
	}
	if cfg.Global.MaxConcurrentRequests != 10 {
		t.Errorf("expected MaxConcurrentRequests=10, got %d", cfg.Global.MaxConcurrentRequests)
	}
	// fields not in the JSON should stay at defaults
	if cfg.Global.CacheDir != ".cache" {
		t.Errorf("expected CacheDir=.cache (default), got %q", cfg.Global.CacheDir)
	}
}

func TestLoadWithInvalidFile(t *testing.T) {
	tmp := t.TempDir()
	origFile := cfgFile
	cfgFile = filepath.Join(tmp, "bad-config.json")
	defer func() { cfgFile = origFile }()

	if err := os.WriteFile(cfgFile, []byte("{invalid json}"), 0644); err != nil {
		t.Fatal(err)
	}

	Load()
	// should fall back to defaults
	if cfg.Global.TimeoutSeconds != 30.0 {
		t.Errorf("expected defaults after invalid parse, got %f", cfg.Global.TimeoutSeconds)
	}
}

func TestLoadWithMissingFile(t *testing.T) {
	tmp := t.TempDir()
	origFile := cfgFile
	cfgFile = filepath.Join(tmp, "nonexistent.json")
	defer func() { cfgFile = origFile }()

	Load()
	// should use defaults without error
	if cfg.Global.TimeoutSeconds != 30.0 {
		t.Errorf("expected defaults, got %f", cfg.Global.TimeoutSeconds)
	}
}

func TestGet(t *testing.T) {
	cfgMu.Lock()
	cfg = defaults()
	cfg.Global.MaxConcurrentRequests = 99
	cfgMu.Unlock()

	got := Get()
	if got.Global.MaxConcurrentRequests != 99 {
		t.Errorf("expected 99, got %d", got.Global.MaxConcurrentRequests)
	}
}

func TestSetReloadHook(t *testing.T) {
	called := false
	SetReloadHook(func() { called = true })
	if reloadHook == nil {
		t.Fatal("expected reloadHook to be set")
	}
	reloadHook()
	if !called {
		t.Error("expected hook to be called")
	}
	SetReloadHook(nil) // reset
}

func TestProviderConfig(t *testing.T) {
	cfg := defaults()
	oc := cfg.Providers["openrouter"]
	if oc.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("expected OpenRouter base URL, got %q", oc.BaseURL)
	}
}
