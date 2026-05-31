package router

import (
	"os"
	"testing"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
	"free-model-router/internal/metrics"
)

func TestMain(m *testing.M) {
	logger.Init(true)
	config.Load()
	metrics.Default = metrics.New()
	os.Exit(m.Run())
}
