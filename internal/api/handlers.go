package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"free-model-router/internal/config"
	"free-model-router/internal/logger"
	met "free-model-router/internal/metrics"
	or "free-model-router/internal/openrouter"
	"free-model-router/internal/router"
)

// SharedHTTPClient is the shared HTTP client with connection pooling.
var SharedHTTPClient *http.Client

// Init sets the package-level globals for handler access.
// Must be called before the server starts.
var (
	AppMetrics   *met.Metrics
	AppFailover  *router.FailoverRouter
	ToolRegistry *or.ToolSupportRegistry
	GetAdapters  func() []or.LLMAdapter
)

func httpClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: SharedHTTPClient.Transport,
	}
}

func getModelsByProvider() map[string][]string {
	mbp := make(map[string][]string)
	for _, adapter := range GetAdapters() {
		models, err := adapter.ListModels()
		if err != nil {
			logger.Error("ListModels failed for %s: %v", adapter.ProviderName(), err)
			continue
		}
		if adapter.ProviderName() == "openrouter" {
			models = ToolRegistry.FilterSupported(models)
		}
		mbp[adapter.ProviderName()] = models
	}
	return mbp
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	// Probe upstream connectivity
	upstreamOK := true
	if SharedHTTPClient != nil {
		resp, err := httpClientWithTimeout(5*time.Second).Get("https://openrouter.ai/api/v1/models")
		if err != nil {
			upstreamOK = false
		} else {
			resp.Body.Close()
			if resp.StatusCode != 200 {
				upstreamOK = false
			}
		}
	}

	status := "ok"
	httpStatus := http.StatusOK
	if !upstreamOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]any{
		"status":   status,
		"upstream": upstreamOK,
		"metrics":  met.Summary(),
		"time":     time.Now().UTC().Format(time.RFC3339),
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	AppMetrics.Mu.RLock()
	modelOut := make(map[string]map[string]any, len(AppMetrics.ModelStats))
	for model, s := range AppMetrics.ModelStats {
		avgLat := float64(0)
		if s.Successes > 0 {
			avgLat = float64(s.TotalLatMs) / float64(s.Successes)
		}
		modelOut[model] = map[string]any{
			"successes":  s.Successes,
			"failures":   s.Failures,
			"avg_lat_ms": math.Round(avgLat),
			"score":      math.Round(met.ScoreModel(model)*100) / 100,
			"last_used":  s.LastUsed,
		}
	}
	// Key stats: only show key number, not the hint (security)
	keyOut := make(map[string]any, len(AppMetrics.KeyStats))
	for _, ks := range AppMetrics.KeyStats {
		keyOut[fmt.Sprintf("#%d", ks.Number)] = map[string]any{
			"successes": ks.Successes,
			"failures":  ks.Failures,
		}
	}
	AppMetrics.Mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"total_requests":  AppMetrics.TotalRequests.Load(),
		"active_requests": AppMetrics.ActiveRequests.Load(),
		"total_errors":    AppMetrics.TotalErrors.Load(),
		"total_successes": AppMetrics.TotalSuccesses.Load(),
		"stream_requests": AppMetrics.StreamRequests.Load(),
		"models":          modelOut,
		"keys":            keyOut,
	})
}

func handleCooldowns(w http.ResponseWriter, r *http.Request) {
	AppFailover.Mu.Lock()
	out := make(map[string]any)
	now := time.Now().Unix()
	for model, exp := range AppFailover.CooldownUntil {
		if rem := exp - now; rem > 0 {
			out[model] = map[string]any{"expires_in_seconds": rem}
		} else {
			delete(AppFailover.CooldownUntil, model)
		}
	}
	AppFailover.Mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	seen := map[string]map[string]any{
		"auto":                   {"id": "auto", "object": "model", "created": now, "owned_by": "free-model-router", "root": "auto"},
		"free-model-router/auto": {"id": "free-model-router/auto", "object": "model", "created": now, "owned_by": "free-model-router", "root": "free-model-router/auto"},
	}
	for _, adapter := range GetAdapters() {
		models, err := adapter.ListModels()
		if err != nil {
			continue
		}
		if adapter.ProviderName() == "openrouter" {
			models = ToolRegistry.FilterSupported(models)
		}
		for _, m := range models {
			if _, ok := seen[m]; !ok {
				seen[m] = map[string]any{
					"id": m, "object": "model", "created": now,
					"owned_by": adapter.ProviderName(), "root": m,
					"score": math.Round(met.ScoreModel(m)*100) / 100,
				}
			}
		}
	}
	list := make([]map[string]any, 0, len(seen))
	for _, v := range seen {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool {
		si, _ := list[i]["score"].(float64)
		sj, _ := list[j]["score"].(float64)
		return si > sj
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": list})
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"method not allowed"}`))
		return
	}

	reqID := reqIDFromCtx(r.Context())
	start := time.Now()

	// Limit request body to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)

	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}

	requestedModel := "auto"
	if m, ok := payload["model"].(string); ok {
		requestedModel = m
	}
	delete(payload, "model")

	isStream := false
	if s, ok := payload["stream"].(bool); ok {
		isStream = s
	}
	if isStream {
		met.IncStreamRequests()
	}

	// Manual override or auto routing
	var modelsByProvider map[string][]string
	isManualOverride := false

	if requestedModel != "auto" && requestedModel != "router/auto" && requestedModel != "" && !strings.Contains(requestedModel, "free-model-router") {
		modelsByProvider = make(map[string][]string)
		modelsByProvider["openrouter"] = []string{requestedModel}
		isManualOverride = true
	} else {
		modelsByProvider = getModelsByProvider()
	}

	anyAvailable := false
	for _, models := range modelsByProvider {
		for _, m := range models {
			if !AppFailover.IsCoolingDown(m) {
				anyAvailable = true
				break
			}
		}
		if anyAvailable {
			break
		}
	}
	// Manual override: if the requested model is on cooldown, still proceed —
	// the failover handler will fall back to auto (free models only)
	if !anyAvailable && !isManualOverride {
		met.IncTotalErrors()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"all models on cooldown, try again later"}`))
		return
	}

	// Non-streaming
	if !isStream {
		res, model, hint, err := AppFailover.ExecuteNonStream(reqID, payload, modelsByProvider)
		if err != nil && isManualOverride {
			// Manual override failed — fall back to auto mode (free models ONLY)
			logger.ReqWarn(reqID, "Manual override model %s failed, falling back to auto (free models only)", requestedModel)
			autoModels := getModelsByProvider()
			res, model, hint, err = AppFailover.ExecuteNonStream(reqID, payload, autoModels)
		}
		if err != nil {
			met.IncTotalErrors()
			logger.ReqError(reqID, "All attempts failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"error":%q}`, err.Error())
			return
		}
		met.IncTotalSuccesses()
		logger.ModelStatus(reqID, "OK", model, hint)
		logger.ReqDebug(reqID, "Done in %s", time.Since(start).Round(time.Millisecond))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
		return
	}

	// Streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	cfg := config.Get()
	budget := cfg.Global.MaxRetriesPerRequest
	slowThreshold := time.Duration(cfg.Global.SlowRequestThresholdMs) * time.Millisecond
	tried := 0
	var lastErr error

	streamModels := AppFailover.RankedModels(modelsByProvider)

	executeStream := func(models []router.AdapterModel) bool {
		for _, am := range models {
			if tried >= budget {
				logger.ReqWarn(reqID, "Retry budget exhausted (%d stream attempts)", tried)
				return false
			}
			if AppFailover.IsCoolingDown(am.Model) {
				logger.ReqDebug(reqID, "Skip stream %s (cooldown %s)",
					am.Model, AppFailover.CooldownRemaining(am.Model).Round(time.Second))
				continue
			}

			tried++
			hist := "(no history)"
			if met.HasHistory(am.Model) {
				hist = fmt.Sprintf("score:%.2f avg:%.0fms", met.ScoreModel(am.Model), met.AvgLatMs(am.Model))
			}
			logger.ReqDebug(reqID, "Stream attempt %d/%d → %s%s%s [%s]",
				tried, budget, logger.ColorCyan, am.Model, logger.ColorReset, hist)

			chunkChan := make(chan []byte, 64)
			resultChan := make(chan or.StreamResult, 1)

			am.Adapter.ChatCompletionStream(payload, am.Model, AppFailover.Timeout, chunkChan, resultChan)

			slowDone := make(chan struct{})
			go func(m string) {
				select {
				case <-time.After(slowThreshold):
					logger.ReqWarn(reqID, "⚠ Slow stream >%s from %s%s%s", slowThreshold, logger.ColorBold, m, logger.ColorReset)
				case <-slowDone:
				}
			}(am.Model)

			sentBytes := false
			for chunk := range chunkChan {
				w.Write(chunk)
				flusher.Flush()
				sentBytes = true
			}
			close(slowDone)

			sr := <-resultChan
			if sr.Err == nil {
				met.IncTotalSuccesses()
				met.RecordSuccess(am.Model, sr.Hint, time.Since(start).Milliseconds())
				logger.ModelStatus(reqID, "OK", am.Model+" [stream]", sr.Hint)
				logger.ReqDebug(reqID, "Stream done in %s", time.Since(start).Round(time.Millisecond))
				return true
			}
			if sentBytes {
				logger.ReqWarn(reqID, "Stream error after partial send from %s: %v", am.Model, sr.Err)
				met.RecordSuccess(am.Model, sr.Hint, time.Since(start).Milliseconds())
				return true
			}

			AppFailover.HandleProviderError(reqID, am.Model, sr.Hint, sr.Err)
			lastErr = sr.Err
		}
		return false
	}

	if !executeStream(streamModels) {
		// Manual override failed — fall back to auto mode (free models ONLY)
		if isManualOverride {
			logger.ReqWarn(reqID, "Manual override stream failed, falling back to auto (free models only)")
			autoModels := AppFailover.RankedModels(getModelsByProvider())
			if executeStream(autoModels) {
				return // auto fallback succeeded
			}
		}
		// All attempts failed
		met.IncTotalErrors()
		errMsg := "all providers failed"
		if lastErr != nil {
			errMsg = lastErr.Error()
		}
		logger.ReqError(reqID, "Stream exhausted: %s", errMsg)
		fmt.Fprintf(w, "data: {\"error\": %q}\n\ndata: [DONE]\n\n", errMsg)
		flusher.Flush()
	}
}
