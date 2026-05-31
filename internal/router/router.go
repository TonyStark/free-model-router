package router

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
	"free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
)

type FailoverRouter struct {
	Mu                      sync.RWMutex
	CloudAdapters           []or.LLMAdapter
	Timeout                 time.Duration
	CooldownSeconds         float64
	NotFoundCooldownSeconds float64
	CooldownUntil           map[string]int64
}

func (fr *FailoverRouter) IsCoolingDown(model string) bool {
	fr.Mu.RLock()
	defer fr.Mu.RUnlock()
	exp, ok := fr.CooldownUntil[model]
	return ok && time.Now().Unix() < exp
}

func (fr *FailoverRouter) CooldownRemaining(model string) time.Duration {
	fr.Mu.RLock()
	defer fr.Mu.RUnlock()
	exp, ok := fr.CooldownUntil[model]
	if !ok {
		return 0
	}
	if rem := time.Until(time.Unix(exp, 0)); rem > 0 {
		return rem
	}
	return 0
}

func (fr *FailoverRouter) MarkCooldown(reqID, model string, seconds float64) {
	fr.Mu.Lock()
	fr.CooldownUntil[model] = time.Now().Unix() + int64(seconds)
	fr.Mu.Unlock()
	logger.ReqWarn(reqID, "Model cooldown %.0fs → %s%s%s", seconds, logger.ColorBold, model, logger.ColorReset)
}

func (fr *FailoverRouter) HandleProviderError(reqID, model, hint string, err error) {
	pe, ok := err.(*or.ProviderError)
	if !ok {
		metrics.RecordFailure(model, hint)
		return
	}
	switch pe.Type {
	case "RateLimitError":
		logger.ReqWarn(reqID, "All keys exhausted for %s%s%s — skipping model", logger.ColorBold, model, logger.ColorReset)
		metrics.RecordFailure(model, hint)
	case "NotFoundError":
		fr.MarkCooldown(reqID, model, fr.NotFoundCooldownSeconds)
		metrics.RecordFailure(model, hint)
	case "AuthError":
		logger.ReqError(reqID, "Auth error on %s: %s", model, pe.Message)
		metrics.RecordFailure(model, hint)
	case "TimeoutError":
		logger.ReqWarn(reqID, "Timeout on %s%s%s", logger.ColorBold, model, logger.ColorReset)
		metrics.RecordFailure(model, hint)
	default:
		logger.ReqError(reqID, "API error on %s: %s", model, pe.Message)
		metrics.RecordFailure(model, hint)
	}
}

type AdapterModel struct {
	Adapter or.LLMAdapter
	Model   string
}

func (fr *FailoverRouter) RankedModels(modelsByProvider map[string][]string) []AdapterModel {
	var all []AdapterModel
	for _, adapter := range fr.CloudAdapters {
		for _, m := range modelsByProvider[adapter.ProviderName()] {
			all = append(all, AdapterModel{adapter, m})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		pI := metrics.GetPriority(all[i].Model)
		pJ := metrics.GetPriority(all[j].Model)
		if pI != pJ {
			return pI < pJ
		}
		return metrics.ScoreModel(all[i].Model) > metrics.ScoreModel(all[j].Model)
	})
	return all
}

func (fr *FailoverRouter) ExecuteNonStream(reqID string, payload map[string]any, modelsByProvider map[string][]string) (map[string]any, string, string, error) {
	cfg := config.Get()
	budget := cfg.Global.MaxRetriesPerRequest
	slowThreshold := time.Duration(cfg.Global.SlowRequestThresholdMs) * time.Millisecond

	ranked := fr.RankedModels(modelsByProvider)
	tried := 0
	var lastErr error

	for _, am := range ranked {
		if tried >= budget {
			logger.ReqWarn(reqID, "Retry budget exhausted (%d attempts)", tried)
			break
		}
		if fr.IsCoolingDown(am.Model) {
			logger.ReqDebug(reqID, "Skip %s (cooldown %s remaining)",
				am.Model, fr.CooldownRemaining(am.Model).Round(time.Second))
			continue
		}

		tried++
		hist := "(no history)"
		if metrics.HasHistory(am.Model) {
			hist = fmt.Sprintf("score:%.2f avg:%.0fms", metrics.ScoreModel(am.Model), metrics.AvgLatMs(am.Model))
		}
		logger.ReqDebug(reqID, "Attempt %d/%d → %s%s%s [%s]",
			tried, budget, logger.ColorCyan, am.Model, logger.ColorReset, hist)

		t0 := time.Now()
		slowDone := make(chan struct{})
		go func(m string) {
			select {
			case <-time.After(slowThreshold):
				logger.ReqWarn(reqID, "⚠ Slow response >%s from %s%s%s", slowThreshold, logger.ColorBold, m, logger.ColorReset)
			case <-slowDone:
			}
		}(am.Model)

		res, hint, err := am.Adapter.ChatCompletion(payload, am.Model, fr.Timeout)
		close(slowDone)
		latMs := time.Since(t0).Milliseconds()

		if err == nil {
			metrics.RecordSuccess(am.Model, hint, latMs)
			return res, am.Model, hint, nil
		}

		fr.HandleProviderError(reqID, am.Model, hint, err)
		lastErr = err
	}

	return nil, "", "", fmt.Errorf("all models failed after %d attempt(s); last: %v", tried, lastErr)
}
