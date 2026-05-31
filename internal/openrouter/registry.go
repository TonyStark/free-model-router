package openrouter

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"free-model-router/internal/logger"
)

type ToolSupportRegistry struct {
	CacheFile string
	Cache     map[string]map[string]any
	mu        sync.RWMutex
}

func NewToolSupportRegistry(file string) *ToolSupportRegistry {
	reg := &ToolSupportRegistry{CacheFile: file, Cache: make(map[string]map[string]any)}
	data, err := os.ReadFile(file)
	if err == nil {
		json.Unmarshal(data, &reg.Cache)
		logger.Debug("Tool support cache loaded: %d entry(s)", len(reg.Cache))
	}
	return reg
}

func (tsr *ToolSupportRegistry) Save() {
	tsr.mu.RLock()
	data, _ := json.MarshalIndent(tsr.Cache, "", "  ")
	tsr.mu.RUnlock()
	os.WriteFile(tsr.CacheFile, data, 0644)
}

func (tsr *ToolSupportRegistry) GetUnverified(models []string) []string {
	tsr.mu.RLock()
	defer tsr.mu.RUnlock()
	var out []string
	for _, m := range models {
		if _, ok := tsr.Cache[m]; !ok {
			out = append(out, m)
		}
	}
	return out
}

func (tsr *ToolSupportRegistry) VerifiedAt(model string) (int64, bool) {
	tsr.mu.RLock()
	defer tsr.mu.RUnlock()
	entry, ok := tsr.Cache[model]
	if !ok {
		return 0, false
	}
	switch v := entry["verified_at"].(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	default:
		return 0, true
	}
}

func (tsr *ToolSupportRegistry) Mark(model string, supported bool) {
	tsr.mu.Lock()
	defer tsr.mu.Unlock()
	tsr.Cache[model] = map[string]any{"tool_support": supported, "verified_at": time.Now().Unix()}
}

func (tsr *ToolSupportRegistry) IsSupported(model string) bool {
	tsr.mu.RLock()
	defer tsr.mu.RUnlock()
	if entry, ok := tsr.Cache[model]; ok {
		if v, ok := entry["tool_support"].(bool); ok {
			return v
		}
	}
	return true
}

func (tsr *ToolSupportRegistry) FilterSupported(models []string) []string {
	var out []string
	for _, m := range models {
		if tsr.IsSupported(m) {
			out = append(out, m)
		}
	}
	return out
}
