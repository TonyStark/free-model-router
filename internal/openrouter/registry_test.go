package openrouter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewToolSupportRegistryNewFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cache.json")
	reg := NewToolSupportRegistry(file)
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	if len(reg.Cache) != 0 {
		t.Errorf("expected empty cache, got %d entries", len(reg.Cache))
	}
	if reg.CacheFile != file {
		t.Errorf("expected CacheFile=%q, got %q", file, reg.CacheFile)
	}
}

func TestNewToolSupportRegistryLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cache.json")
	existing := `{"gpt-4":{"tool_support":true,"verified_at":1700000000}}`
	if err := os.WriteFile(file, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewToolSupportRegistry(file)
	if len(reg.Cache) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(reg.Cache))
	}
	if _, ok := reg.Cache["gpt-4"]; !ok {
		t.Error("expected gpt-4 in cache")
	}
}

func TestGetUnverified(t *testing.T) {
	reg := NewToolSupportRegistry("")
	reg.Cache["known-model"] = map[string]any{"tool_support": true}
	reg.Cache["another-known"] = map[string]any{"tool_support": false}

	models := []string{"known-model", "unknown-model", "another-known", "new-model"}
	unverified := reg.GetUnverified(models)

	if len(unverified) != 2 {
		t.Errorf("expected 2 unverified, got %d: %v", len(unverified), unverified)
	}
	if unverified[0] != "unknown-model" {
		t.Errorf("expected unknown-model first, got %q", unverified[0])
	}
	if unverified[1] != "new-model" {
		t.Errorf("expected new-model second, got %q", unverified[1])
	}
}

func TestGetUnverifiedEmpty(t *testing.T) {
	reg := NewToolSupportRegistry("")
	unverified := reg.GetUnverified(nil)
	if len(unverified) != 0 {
		t.Errorf("expected 0, got %d", len(unverified))
	}

	unverified = reg.GetUnverified([]string{})
	if len(unverified) != 0 {
		t.Errorf("expected 0, got %d", len(unverified))
	}
}

func TestMarkAndIsSupported(t *testing.T) {
	reg := NewToolSupportRegistry("")

	reg.Mark("model-alpha", true)
	if !reg.IsSupported("model-alpha") {
		t.Error("expected model-alpha to be supported")
	}

	reg.Mark("model-beta", false)
	if reg.IsSupported("model-beta") {
		t.Error("expected model-beta to be NOT supported")
	}
}

func TestIsSupportedDefaultsToTrue(t *testing.T) {
	reg := NewToolSupportRegistry("")
	if !reg.IsSupported("unknown-model") {
		t.Error("expected unknown model to default to supported")
	}
}

func TestFilterSupported(t *testing.T) {
	reg := NewToolSupportRegistry("")
	reg.Mark("model-a", true)
	reg.Mark("model-b", false)
	reg.Mark("model-c", true)

	input := []string{"model-a", "model-b", "model-c", "model-d"}
	filtered := reg.FilterSupported(input)

	expected := []string{"model-a", "model-c", "model-d"}
	if len(filtered) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, filtered)
	}
	for i := range expected {
		if filtered[i] != expected[i] {
			t.Errorf("index %d: expected %q, got %q", i, expected[i], filtered[i])
		}
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tool_cache.json")

	reg := NewToolSupportRegistry(file)
	reg.Mark("model-x", true)
	reg.Mark("model-y", false)
	reg.Save()

	// reload into a new registry
	reg2 := NewToolSupportRegistry(file)
	if len(reg2.Cache) != 2 {
		t.Fatalf("expected 2 entries after reload, got %d", len(reg2.Cache))
	}
	if !reg2.IsSupported("model-x") {
		t.Error("expected model-x supported after reload")
	}
	if reg2.IsSupported("model-y") {
		t.Error("expected model-y unsupported after reload")
	}
}

func TestVerifiedAt(t *testing.T) {
	reg := NewToolSupportRegistry("")
	_, ok := reg.VerifiedAt("never-verified")
	if ok {
		t.Error("expected false for unverified model")
	}

	reg.Mark("just-verified", true)
	ts, ok := reg.VerifiedAt("just-verified")
	if !ok {
		t.Error("expected true for verified model")
	}
	if ts <= 0 {
		t.Errorf("expected valid timestamp, got %d", ts)
	}
}
