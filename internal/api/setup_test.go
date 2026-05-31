package api

import (
	"os"
	"testing"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
	"free-model-router/internal/router"
)

func TestMain(m *testing.M) {
	logger.Init(true)
	config.Load()
	met.Default = met.New()
	AppMetrics = met.Default
	AppFailover = &router.FailoverRouter{CooldownUntil: make(map[string]int64)}
	ToolRegistry = or.NewToolSupportRegistry("")
	code := m.Run()
	os.Exit(code)
}
