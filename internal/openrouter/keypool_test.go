package openrouter

import (
	"errors"
	"testing"
	"time"
)

func TestNewKeyPool(t *testing.T) {
	keys := []string{"sk-or-v1-abcdef123456", "sk-or-v1-xyz789000000"}
	kp := NewKeyPool(keys)
	if kp == nil {
		t.Fatal("expected non-nil KeyPool")
	}
	if len(kp.keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(kp.keys))
	}
	if len(kp.hints) != 2 {
		t.Errorf("expected 2 hints, got %d", len(kp.hints))
	}
	if kp.keyCooldowns == nil {
		t.Error("expected non-nil cooldown map")
	}
}

func TestNewKeyPoolEmpty(t *testing.T) {
	kp := NewKeyPool(nil)
	if kp == nil {
		t.Fatal("expected non-nil KeyPool")
	}
	if len(kp.keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(kp.keys))
	}
}

func TestBuildHint(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"short", "…short"},
		{"abcdef", "…abcdef"},
		{"sk-or-v1-abcdef123456", "…123456"},
		{"sk-or-v1-xyz789000000", "…000000"},
	}
	for _, tc := range tests {
		hint := BuildHint(tc.key)
		if hint != tc.expected {
			t.Errorf("BuildHint(%q) = %q, want %q", tc.key, hint, tc.expected)
		}
	}
}

func TestTryAllKeysFirstSucceeds(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	hint, err := kp.TryAllKeys(false, "model-a", func(key, hint string, keyNum int) error {
		if key != "key1" {
			t.Errorf("expected first key, got %q", key)
		}
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if hint == "" {
		t.Error("expected non-empty hint")
	}
}

func TestTryAllKeysEmptyPool(t *testing.T) {
	kp := NewKeyPool(nil)
	_, err := kp.TryAllKeys(false, "model-a", func(key, hint string, keyNum int) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for empty pool")
	}
}

func TestTryAllKeysPermanentError(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	permErr := errors.New("permanent error")
	_, err := kp.TryAllKeys(false, "model-a", func(key, hint string, keyNum int) error {
		return permErr
	})
	if err != permErr {
		t.Errorf("expected permanent error, got %v", err)
	}
}

func TestTryAllKeysFallthroughOnRateLimit(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	keyOrder := []string{}
	_, err := kp.TryAllKeys(true, "model-a", func(key, hint string, keyNum int) error {
		keyOrder = append(keyOrder, key)
		if keyNum == 1 {
			return &ProviderError{Type: "RateLimitError", Message: "rate limited"}
		}
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(keyOrder) != 2 {
		t.Errorf("expected 2 attempts, got %d", len(keyOrder))
	}
	if keyOrder[0] != "key1" || keyOrder[1] != "key2" {
		t.Errorf("expected order [key1, key2], got %v", keyOrder)
	}
}

func TestTryAllKeysAllRateLimited(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	_, err := kp.TryAllKeys(true, "model-a", func(key, hint string, keyNum int) error {
		return &ProviderError{Type: "RateLimitError", Message: "rate limited"}
	})
	if err == nil {
		t.Fatal("expected error when all keys rate-limited")
	}
}

func TestTryAllKeysAllOnCooldown(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	// manually mark both keys on cooldown for model-a
	kp.markCooldown(kp.hints[0], "model-a", 3600)
	kp.markCooldown(kp.hints[1], "model-a", 3600)

	attempted := false
	_, err := kp.TryAllKeys(true, "model-a", func(key, hint string, keyNum int) error {
		attempted = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error when all keys on cooldown")
	}
	if attempted {
		t.Error("no attempt should be made when all keys are on cooldown")
	}
}

func TestTryAllKeysRateLimitPutsOnCooldown(t *testing.T) {
	kp := NewKeyPool([]string{"key1"})
	hint := kp.hints[0]

	// first call: rate-limited and cooldown set
	_, _ = kp.TryAllKeys(true, "model-x", func(key, hint string, keyNum int) error {
		return &ProviderError{Type: "RateLimitError", Message: "too fast"}
	})

	// immediately verify cooldown is active
	if !kp.isKeyCooling(hint, "model-x") {
		t.Error("expected key to be on cooldown after rate limit")
	}
}

func TestTryAllKeysProviderErrorStops(t *testing.T) {
	kp := NewKeyPool([]string{"key1", "key2"})
	attempts := 0
	_, err := kp.TryAllKeys(false, "model-a", func(key, hint string, keyNum int) error {
		attempts++
		return &ProviderError{Type: "NotFoundError", Message: "model not found"}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*ProviderError); !ok || pe.Type != "NotFoundError" {
		t.Errorf("expected ProviderError(NotFoundError), got %T(%v)", err, err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt before non-rate-limit error, got %d", attempts)
	}
}

func TestNextReturnsFirstAvailable(t *testing.T) {
	kp := NewKeyPool([]string{"key-a", "key-b"})
	key, hint, num := kp.Next("any-model")
	if key != "key-a" {
		t.Errorf("expected key-a, got %q", key)
	}
	if num != 1 {
		t.Errorf("expected num=1, got %d", num)
	}
	if hint == "" {
		t.Error("expected non-empty hint")
	}
}

func TestNextSkipsCooledKeys(t *testing.T) {
	kp := NewKeyPool([]string{"key-a", "key-b"})
	kp.markCooldown(kp.hints[0], "model-z", 3600)

	key, _, num := kp.Next("model-z")
	if key != "key-b" {
		t.Errorf("expected key-b (skip cooled key-a), got %q", key)
	}
	if num != 2 {
		t.Errorf("expected num=2, got %d", num)
	}
}

func TestNextAllCooledReturnsFirst(t *testing.T) {
	kp := NewKeyPool([]string{"key-a", "key-b"})
	kp.markCooldown(kp.hints[0], "model-z", 3600)
	kp.markCooldown(kp.hints[1], "model-z", 3600)

	key, _, _ := kp.Next("model-z")
	if key != "key-a" {
		t.Errorf("expected key-a (first even when cooled), got %q", key)
	}
}

func TestNextEmptyPool(t *testing.T) {
	kp := NewKeyPool(nil)
	key, hint, num := kp.Next("anything")
	if key != "" || hint != "" || num != 0 {
		t.Errorf("expected empty result, got key=%q hint=%q num=%d", key, hint, num)
	}
}

func TestIsKeyCooling(t *testing.T) {
	kp := NewKeyPool([]string{"test-key"})
	hint := kp.hints[0]

	if kp.isKeyCooling(hint, "model-a") {
		t.Error("expected no cooldown initially")
	}

	kp.markCooldown(hint, "model-a", 3600)
	if !kp.isKeyCooling(hint, "model-a") {
		t.Error("expected cooldown after marking")
	}
}

func TestIsKeyCoolingExpired(t *testing.T) {
	kp := NewKeyPool([]string{"test-key"})
	hint := kp.hints[0]

	// set cooldown in the past
	kp.mu.Lock()
	kp.keyCooldowns[hint+"|model-a"] = time.Now().Unix() - 1
	kp.mu.Unlock()

	if kp.isKeyCooling(hint, "model-a") {
		t.Error("expected no cooldown for expired entry")
	}
}
