package openrouter

import (
	"os"
	"testing"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(true)
	config.Load()
	os.Exit(m.Run())
}
