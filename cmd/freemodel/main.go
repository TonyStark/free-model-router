package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"free-model-router/internal/api"
	"free-model-router/internal/config"
	"free-model-router/internal/logger"
	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
	"free-model-router/internal/router"
)

var (
	cloudAdapters     []or.LLMAdapter
	adaptersMu        sync.RWMutex
	failover          *router.FailoverRouter
	toolRegistry      *or.ToolSupportRegistry
	openRouterAdapter *or.OpenRouterAdapter
	appMet            = met.New()
)

func buildOpenRouterAdapter(cfg config.Config) *or.OpenRouterAdapter {
	keyEnv := os.Getenv("OPENROUTER_API_KEYS")
	var keys []string
	for _, k := range strings.Split(keyEnv, ",") {
		if clean := strings.TrimSpace(k); clean != "" {
			keys = append(keys, clean)
		}
	}
	if len(keys) == 0 {
		logger.Warn("No OPENROUTER_API_KEYS set — requests will fail auth")
	}

	mr := &or.ModelRouter{
		BaseURL:         cfg.Providers["openrouter"].BaseURL,
		ExcludeKeywords: cfg.Providers["openrouter"].ExcludeKeywords,
		CacheTTL:        cfg.Global.ModelCacheTTLSeconds,
	}
	a := &or.OpenRouterAdapter{
		KeyPool:     or.NewKeyPool(keys),
		BaseURL:     cfg.Providers["openrouter"].BaseURL,
		ModelRouter: mr,
	}

	logger.Info("OpenRouter: %s%d%s key(s) loaded", logger.ColorMagenta+logger.ColorBold, len(keys), logger.ColorReset)
	for i, k := range keys {
		h := or.BuildHint(k)
		met.RegisterKey(h, i+1)
		logger.Debug("  Key #%d: %s", i+1, h)
	}
	return a
}

func rebuildAdapters() {
	cfg := config.Get()
	var adapters []or.LLMAdapter
	for _, p := range cfg.EnabledProviders {
		if p == "openrouter" {
			a := buildOpenRouterAdapter(cfg)
			openRouterAdapter = a
			adapters = append(adapters, a)
		}
	}
	adaptersMu.Lock()
	cloudAdapters = adapters
	adaptersMu.Unlock()

	failover.Mu.Lock()
	failover.CloudAdapters = adapters
	failover.Timeout = time.Duration(cfg.Global.TimeoutSeconds) * time.Second
	failover.CooldownSeconds = cfg.Global.RateLimitCooldownSeconds
	failover.NotFoundCooldownSeconds = cfg.Global.NotFoundCooldownSeconds
	failover.Mu.Unlock()

	logger.Info("Adapters rebuilt (%d provider(s))", len(adapters))
}

func getAdapters() []or.LLMAdapter {
	adaptersMu.RLock()
	defer adaptersMu.RUnlock()
	return cloudAdapters
}

func printModelTable(modelsByProvider map[string][]string) {
	header := []string{
		logger.ColorGray + "#" + logger.ColorReset,
		logger.ColorGray + "Provider" + logger.ColorReset,
		logger.ColorGray + "Model" + logger.ColorReset,
		logger.ColorGray + "Score" + logger.ColorReset,
		logger.ColorGray + "Avg Lat" + logger.ColorReset,
		logger.ColorGray + "Tools" + logger.ColorReset,
		logger.ColorGray + "Status" + logger.ColorReset,
	}
	var rows [][]string
	i := 0
	for provider, models := range modelsByProvider {
		for _, m := range met.SortByScore(models) {
			i++
			scoreStr := logger.ColorGray + "–" + logger.ColorReset
			latStr := logger.ColorGray + "–" + logger.ColorReset
			if met.HasHistory(m) {
				s := met.ScoreModel(m)
				c := logger.ColorGreen
				if s < 0.5 {
					c = logger.ColorRed
				} else if s < 0.75 {
					c = logger.ColorYellow
				}
				scoreStr = fmt.Sprintf("%s%.2f%s", c, s, logger.ColorReset)
				latStr = fmt.Sprintf("%.0fms", met.AvgLatMs(m))
			}
			toolStr := logger.ColorGray + "?" + logger.ColorReset
			if _, cached := toolRegistry.VerifiedAt(m); cached {
				if toolRegistry.IsSupported(m) {
					toolStr = logger.ColorGreen + "✓" + logger.ColorReset
				} else {
					toolStr = logger.ColorYellow + "✗" + logger.ColorReset
				}
			}
			statusStr := logger.ColorGreen + "ready" + logger.ColorReset
			if failover.IsCoolingDown(m) {
				rem := failover.CooldownRemaining(m).Round(time.Second)
				statusStr = fmt.Sprintf("%s⏸ %s%s", logger.ColorRed, rem, logger.ColorReset)
			}
			rows = append(rows, []string{
				logger.ColorGray + fmt.Sprintf("#%d", i) + logger.ColorReset,
				logger.ColorCyan + provider + logger.ColorReset,
				m,
				scoreStr,
				latStr,
				toolStr,
				statusStr,
			})
		}
	}
	logger.Banner("Available Models", header, rows)
}

func getAdaptersMap() map[string][]string {
	mbp := make(map[string][]string)
	for _, adapter := range getAdapters() {
		models, err := adapter.ListModels()
		if err != nil {
			logger.Error("ListModels failed for %s: %v", adapter.ProviderName(), err)
			continue
		}
		mbp[adapter.ProviderName()] = models
	}
	return mbp
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func main() {
	debugFlag := flag.Bool("debug", false, "Enable verbose debug logging")
	portFlag := flag.String("port", "", "Override SERVER_PORT")
	hostFlag := flag.String("host", "", "Override SERVER_HOST")
	flag.Parse()

	logger.Init(*debugFlag)
	if *debugFlag {
		logger.Debug("Debug mode %sENABLED%s", logger.ColorBold, logger.ColorReset)
	}

	config.SetReloadHook(func() { rebuildAdapters() })
	config.LoadEnv()
	config.Load()
	met.Default = appMet

	cfg := config.Get()
	os.MkdirAll(cfg.Global.CacheDir, 0755)
	toolRegistry = or.NewToolSupportRegistry(filepath.Join(cfg.Global.CacheDir, "tool_support_cache.json"))

	scorePath := filepath.Join(cfg.Global.CacheDir, cfg.Global.ScoreCacheFile)
	appMet.LoadScoreCache(scorePath)

	var adapters []or.LLMAdapter
	for _, p := range cfg.EnabledProviders {
		if p == "openrouter" {
			a := buildOpenRouterAdapter(cfg)
			openRouterAdapter = a
			adapters = append(adapters, a)
		}
	}
	adaptersMu.Lock()
	cloudAdapters = adapters
	adaptersMu.Unlock()

	failover = &router.FailoverRouter{
		CloudAdapters:           adapters,
		Timeout:                 time.Duration(cfg.Global.TimeoutSeconds) * time.Second,
		CooldownSeconds:         cfg.Global.RateLimitCooldownSeconds,
		NotFoundCooldownSeconds: cfg.Global.NotFoundCooldownSeconds,
		CooldownUntil:           make(map[string]int64),
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	verDone := make(chan struct{})
	go func() {
		or.RunToolVerification(bgCtx, openRouterAdapter, toolRegistry)
		close(verDone)
	}()
	go config.WatchReload(bgCtx)

	go func() {
		select {
		case <-verDone:
		case <-time.After(time.Duration(cfg.Global.VerifyTimeoutSeconds+15) * time.Second):
			logger.Warn("Verification timed out — printing model table with partial results")
		}
		printModelTable(getAdaptersMap())
	}()

	if *debugFlag {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-bgCtx.Done():
					return
				case <-ticker.C:
					printModelTable(getAdaptersMap())
				}
			}
		}()
	}

	handler := api.New(appMet, failover, toolRegistry, func() []or.LLMAdapter {
		adaptersMu.RLock()
		defer adaptersMu.RUnlock()
		return cloudAdapters
	})

	port := firstNonEmpty(*portFlag, os.Getenv("SERVER_PORT"), "4141")
	host := firstNonEmpty(*hostFlag, os.Getenv("SERVER_HOST"), "0.0.0.0")
	addr := fmt.Sprintf("%s:%s", host, port)

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  time.Duration(cfg.Global.TimeoutSeconds+5) * time.Second,
		WriteTimeout: time.Duration(cfg.Global.TimeoutSeconds+15) * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("Listening on %shttp://%s%s  (%sCTRL+C%s to quit | %sSIGHUP%s to reload config)",
			logger.ColorCyan+logger.ColorBold, addr, logger.ColorReset,
			logger.ColorYellow, logger.ColorReset, logger.ColorYellow, logger.ColorReset,
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("ListenAndServe: %v", err)
			os.Exit(1)
		}
	}()

	sig := <-quit
	logger.Warn("Signal %q — draining %d active request(s)…", sig, met.ActiveCount())
	bgCancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.Global.ShutdownTimeoutSeconds)*time.Second)
	defer shutCancel()

	if err := server.Shutdown(shutCtx); err != nil {
		logger.Error("Forced shutdown after timeout: %v", err)
	} else {
		logger.Info("Server drained cleanly")
	}

	toolRegistry.Save()
	appMet.SaveScoreCache(scorePath)
	logger.Info("Goodbye!  %s", met.Summary())
}
