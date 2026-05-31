package openrouter

import (
	"fmt"
	"sync"
	"time"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
)

type KeyPool struct {
	keys         []string
	hints        []string
	mu           sync.RWMutex
	keyCooldowns map[string]int64
}

func NewKeyPool(keys []string) *KeyPool {
	hints := make([]string, len(keys))
	for i, k := range keys {
		hints[i] = BuildHint(k)
	}
	return &KeyPool{
		keys:         keys,
		hints:        hints,
		keyCooldowns: make(map[string]int64),
	}
}

func BuildHint(k string) string {
	if len(k) <= 6 {
		return k
	}
	return "…" + k[len(k)-6:]
}

func (kp *KeyPool) markCooldown(hint, model string, seconds float64) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kp.keyCooldowns[hint+"|"+model] = time.Now().Unix() + int64(seconds)
}

func (kp *KeyPool) isKeyCooling(hint, model string) bool {
	kp.mu.RLock()
	defer kp.mu.RUnlock()
	exp, ok := kp.keyCooldowns[hint+"|"+model]
	return ok && time.Now().Unix() < exp
}

func (kp *KeyPool) TryAllKeys(persistent bool, model string, fn func(key, hint string, keyNum int) error) (string, error) {
	n := len(kp.keys)
	if n == 0 {
		return "", fmt.Errorf("no API keys configured")
	}
	var lastErr error
	tried := 0
	usedHint := ""

	for i := 0; i < n; i++ {
		h := kp.hints[i]
		if kp.isKeyCooling(h, model) {
			continue
		}
		tried++
		err := fn(kp.keys[i], h, i+1)
		if err == nil {
			usedHint = h
			return usedHint, nil
		}
		lastErr = err
		if pe, ok := err.(*ProviderError); ok && pe.Type == "RateLimitError" {
			if persistent {
				kp.markCooldown(h, model, config.Get().Global.RateLimitCooldownSeconds)
				logger.Debug("Key %s rate-limited on %s, cooling for %.0fs", h, model, config.Get().Global.RateLimitCooldownSeconds)
			}
			continue
		}
		return "", err
	}
	if tried == 0 {
		return "", fmt.Errorf("all %d keys are on cooldown for model %s", n, model)
	}
	return "", lastErr
}

func (kp *KeyPool) Next(model string) (key, hint string, keyNum int) {
	n := len(kp.keys)
	if n == 0 {
		return "", "", 0
	}
	for i, h := range kp.hints {
		if !kp.isKeyCooling(h, model) {
			return kp.keys[i], h, i + 1
		}
	}
	return kp.keys[0], kp.hints[0], 1
}
