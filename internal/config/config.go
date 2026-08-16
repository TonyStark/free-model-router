package config

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"free-model-router/internal/logger"
)

type GlobalConfig struct {
	VerifyToolSupport            bool    `json:"verify_tool_support"`
	VerifyTimeoutSeconds         float64 `json:"verify_timeout_seconds"`
	VerifyConcurrency            int     `json:"verify_concurrency"`
	ModelCacheTTLSeconds         int     `json:"model_cache_ttl_seconds"`
	TimeoutSeconds               float64 `json:"timeout_seconds"`
	RateLimitCooldownSeconds     float64 `json:"rate_limit_cooldown_seconds"`
	NotFoundCooldownSeconds      float64 `json:"not_found_cooldown_seconds"`
	AuthCooldownSeconds          float64 `json:"auth_cooldown_seconds"`
	CacheDir                     string  `json:"cache_dir"`
	MaxConcurrentRequests        int     `json:"max_concurrent_requests"`
	ShutdownTimeoutSeconds       int     `json:"shutdown_timeout_seconds"`
	MaxRetriesPerRequest         int     `json:"max_retries_per_request"`
	SlowRequestThresholdMs       int     `json:"slow_request_threshold_ms"`
	ScoreCacheFile               string  `json:"score_cache_file"`
	MetadataWeightNoHistory      float64 `json:"metadata_weight_no_history"`
	MetadataWeightWithHistory    float64 `json:"metadata_weight_with_history"`
	TopModelPoolSize              int      `json:"top_model_pool_size"`
	MinModelAttemptsForConfidence int      `json:"min_model_attempts_for_confidence"`
	AllowedModels                 []string `json:"allowed_models"`
}

type ProviderConfig struct {
	BaseURL          string   `json:"base_url"`
	PriorityKeywords []string `json:"priority_keywords"`
	ExcludeKeywords  []string `json:"exclude_keywords"`
}

type Config struct {
	Global           GlobalConfig              `json:"global"`
	EnabledProviders []string                  `json:"enabled_providers"`
	Providers        map[string]ProviderConfig `json:"providers"`
}

var (
	cfg        Config
	cfgMu      sync.RWMutex
	cfgFile    = "config.json"
	reloadHook func()
)

func SetReloadHook(fn func()) { reloadHook = fn }

func defaults() Config {
	return Config{
		Global: GlobalConfig{
			VerifyToolSupport:            true,
			VerifyTimeoutSeconds:         30.0,
			VerifyConcurrency:            2,
			ModelCacheTTLSeconds:         300,
			TimeoutSeconds:               30.0,
			RateLimitCooldownSeconds:     60.0,
			NotFoundCooldownSeconds:      3600.0,
			AuthCooldownSeconds:          600.0,
			CacheDir:                     ".cache",
			MaxConcurrentRequests:        50,
			ShutdownTimeoutSeconds:       20,
			MaxRetriesPerRequest:         3,
			SlowRequestThresholdMs:       8000,
			ScoreCacheFile:               "score_cache.json",
			MetadataWeightNoHistory:      0.85,
			MetadataWeightWithHistory:    0.35,
			TopModelPoolSize:             5,
			MinModelAttemptsForConfidence: 8,
		},
		EnabledProviders: []string{"openrouter"},
		Providers: map[string]ProviderConfig{
			"openrouter": {BaseURL: "https://openrouter.ai/api/v1"},
		},
	}
}

func LoadEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			// Strip surrounding quotes (single or double)
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			// Don't overwrite existing environment variables
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

func Load() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	cfg = defaults()
	data, err := os.ReadFile(cfgFile)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.Warn("config.json parse error: %v — using defaults", err)
		} else {
			logger.Info("Loaded %s", cfgFile)
		}
	} else {
		logger.Warn("config.json not found, using defaults")
	}
}

func Get() Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg
}

func WatchReload(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			logger.Info("SIGHUP — reloading config…")
			Load()
			if reloadHook != nil {
				reloadHook()
			}
			logger.Info("Config hot-reload complete")
		}
	}
}
