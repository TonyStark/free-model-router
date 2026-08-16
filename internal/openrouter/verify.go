package openrouter

import (
	"context"
	"sync"
	"time"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
)

func VerifyToolSupport(ctx context.Context, adapter *OpenRouterAdapter, model string, timeoutSecs float64) *bool {
	payload := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "What is the weather in Tokyo? You MUST call get_weather."},
		},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather for a city",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"city": map[string]any{"type": "string"}},
					"required":   []string{"city"},
				},
			},
		}},
		"tool_choice": "auto",
		"max_tokens":  64,
		"temperature": 0,
	}

	result := make(chan *bool, 1)
	go func() {
		resp, err := adapter.ChatCompletionSingleKey(ctx, payload, model, time.Duration(timeoutSecs)*time.Second)
		if err != nil {
			if pe, ok := err.(*ProviderError); ok && (pe.Type == "NotFoundError" || pe.Type == "AuthError") {
				f := false
				result <- &f
				return
			}
			result <- nil
			return
		}
		choices, ok := resp["choices"].([]any)
		if !ok || len(choices) == 0 {
			f := false
			result <- &f
			return
		}
		msg, _ := choices[0].(map[string]any)["message"].(map[string]any)
		_, hasTool := msg["tool_calls"]
		result <- &hasTool
	}()

	select {
	case <-ctx.Done():
		return nil
	case r := <-result:
		return r
	}
}

func RunToolVerification(ctx context.Context, adapter *OpenRouterAdapter, registry *ToolSupportRegistry) {
	cfg := config.Get()
	if !cfg.Global.VerifyToolSupport || adapter == nil {
		return
	}

	models, err := adapter.ListModels()
	if err != nil {
		logger.Error("Verification: ListModels failed: %v", err)
		return
	}
	unverified := registry.GetUnverified(models)
	if len(unverified) == 0 {
		logger.Info("Tool support cache up to date (%d model(s) already verified)", len(models))
		return
	}

	concurrency := cfg.Global.VerifyConcurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	logger.Info("Verifying %d new model(s) — concurrency=%d, timeout=%.0fs each",
		len(unverified), concurrency, cfg.Global.VerifyTimeoutSeconds)

	batches := (len(unverified) + concurrency - 1) / concurrency
	overallDeadline := time.Duration(float64(batches)*cfg.Global.VerifyTimeoutSeconds+10) * time.Second
	verCtx, cancel := context.WithTimeout(ctx, overallDeadline)
	defer cancel()

	type vResult struct {
		model string
		res   *bool
	}
	results := make(chan vResult, len(unverified))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for _, m := range unverified {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := VerifyToolSupport(verCtx, adapter, model, cfg.Global.VerifyTimeoutSeconds)
			results <- vResult{model, r}
		}(m)
	}
	go func() { wg.Wait(); close(results) }()

	header := []string{
		logger.ColorGray + "Status" + logger.ColorReset,
		logger.ColorGray + "Model" + logger.ColorReset,
		logger.ColorGray + "Result" + logger.ColorReset,
		logger.ColorGray + "Source" + logger.ColorReset,
	}
	var rows [][]string

	for r := range results {
		if r.res != nil {
			registry.Mark(r.model, *r.res)
			if *r.res {
				rows = append(rows, []string{logger.ColorGreen + "✓" + logger.ColorReset, r.model, logger.ColorGreen + "tools ok" + logger.ColorReset, logger.ColorCyan + "fresh" + logger.ColorReset})
			} else {
				rows = append(rows, []string{logger.ColorYellow + "✗" + logger.ColorReset, r.model, logger.ColorYellow + "no tools" + logger.ColorReset, logger.ColorCyan + "fresh" + logger.ColorReset})
			}
		} else {
			registry.Mark(r.model, false)
			rows = append(rows, []string{logger.ColorYellow + "✗" + logger.ColorReset, r.model, logger.ColorYellow + "no tools (timeout)" + logger.ColorReset, logger.ColorCyan + "fresh" + logger.ColorReset})
		}
	}

	for _, m := range models {
		if _, cached := registry.VerifiedAt(m); !cached {
			continue
		}
		alreadyShown := false
		for _, row := range rows {
			if row[1] == m {
				alreadyShown = true
				break
			}
		}
		if alreadyShown {
			continue
		}
		if registry.IsSupported(m) {
			rows = append(rows, []string{logger.ColorGreen + "✓" + logger.ColorReset, m, logger.ColorGreen + "tools ok" + logger.ColorReset, logger.ColorGray + "cached" + logger.ColorReset})
		} else {
			rows = append(rows, []string{logger.ColorYellow + "✗" + logger.ColorReset, m, logger.ColorYellow + "no tools" + logger.ColorReset, logger.ColorGray + "cached" + logger.ColorReset})
		}
	}

	registry.Save()
	logger.Banner("Tool Support Verification", header, rows)
}
