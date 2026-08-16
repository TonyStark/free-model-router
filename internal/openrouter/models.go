package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"free-model-router/internal/logger"
)

type ModelRouter struct {
	BaseURL         string
	ExcludeKeywords []string
	AllowedModels   []string
	CacheTTL        int
	cachedModels    []string
	cacheTime       int64
	mu              sync.RWMutex
}

func (mr *ModelRouter) GetFreeModels() ([]string, error) {
	mr.mu.RLock()
	if mr.cachedModels != nil && time.Now().Unix()-mr.cacheTime < int64(mr.CacheTTL) {
		m := mr.cachedModels
		mr.mu.RUnlock()
		return m, nil
	}
	mr.mu.RUnlock()

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(mr.BaseURL + "/models")
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&data)

	var free []string
	for _, m := range data.Data {
		if !strings.Contains(m.ID, ":free") || m.Pricing.Prompt != "0" || m.Pricing.Completion != "0" {
			continue
		}
		excluded := false
		for _, ex := range mr.ExcludeKeywords {
			if strings.Contains(strings.ToLower(m.ID), strings.ToLower(ex)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		// Allowlist filter: if set, only include models in the list
		if len(mr.AllowedModels) > 0 {
			allowed := false
			for _, a := range mr.AllowedModels {
				if strings.EqualFold(m.ID, a) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		free = append(free, m.ID)
	}

	mr.mu.Lock()
	mr.cachedModels = free
	mr.cacheTime = time.Now().Unix()
	mr.mu.Unlock()

	logger.Debug("Model cache refreshed: %d free models", len(free))
	return free, nil
}
