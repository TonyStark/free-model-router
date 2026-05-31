package config

import (
	"os"
	"testing"

	"free-model-router/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(true)
	Load()
	os.Exit(m.Run())
}
