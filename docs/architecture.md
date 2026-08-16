# Architecture

## Repository Structure

```
free-model-router/
├── cmd/
│   └── freemodel/
│       └── main.go              # Entrypoint: flag parsing, wiring, signal handling
├── internal/
│   ├── api/
│   │   ├── server.go            # New() factory — mux, routes, middleware chain
│   │   ├── handlers.go          # All 5 HTTP handlers + package globals
│   │   └── middleware.go        # Logging, rate-limit, CORS, request ID
│   ├── config/
│   │   └── config.go            # Config struct, Load(), Get(), WatchReload()
│   ├── logger/
│   │   └── logger.go            # Singleton ANSI-logger with colored output
│   ├── metrics/
│   │   ├── metrics.go           # Metrics struct, per-model/key stats, atomic counters
│   │   └── scorer.go            # ScoreModel(), GetPriority(), SortByScore()
│   ├── openrouter/
│   │   ├── adapter.go           # LLMAdapter interface, OpenRouterAdapter, ProviderError
│   │   ├── keypool.go           # KeyPool with multi-key rotation and per-model cooldown
│   │   ├── models.go            # ModelRouter – fetches free models with TTL cache
│   │   ├── registry.go          # ToolSupportRegistry – persisted tool-support cache
│   │   └── verify.go            # Tool-call probing at startup
│   └── router/
│       └── router.go            # FailoverRouter – ranking, cooldowns, execute
├── config.json                  # Runtime configuration
├── .env                         # Environment variables (API keys)
├── go.mod                       # Module definition
├── README.md
└── docs/
    └── architecture.md          # This file
```

## Dependency Graph

```
cmd/freemodel/main.go
  ├── internal/api         ──→ config, logger, metrics, openrouter, router
  ├── internal/config      ──→ logger
  ├── internal/logger       (no dependencies)
  ├── internal/metrics     ──→ logger, config
  ├── internal/openrouter  ──→ logger, config
  └── internal/router      ──→ logger, config, metrics, openrouter
```

Key design decisions:

- **`logger` is leaf-level** — no dependencies, safe to import from every package
- **`openrouter` provides the `LLMAdapter` interface** — not a separate package, since only one concrete type exists. Extract when a second adapter appears.
- **`scorer` lives in `metrics`** — scoring is a read-model query on metrics data; keeps package count low
- **Circular dependency avoided** — `config.SetReloadHook()` callback pattern prevents `config → router → config`
- **`api.New()` takes a closure** — `func() []or.LLMAdapter` instead of storing a slice directly, prevents stale adapter references after SIGHUP reload
- **Shared HTTP client** — connection pooling via `http.Transport{MaxIdleConns:100, MaxIdleConnsPerHost:20, IdleConnTimeout:90s}`, shared across all adapter requests
- **CORS enabled** — `Access-Control-Allow-Origin: *` for browser clients

## Configuration

### Global Config Keys

| Key | Default | Description |
|-----|---------|-------------|
| `verify_tool_support` | `true` | Probe new models for tool-call support at startup |
| `verify_timeout_seconds` | `30` | Timeout per verification probe |
| `verify_concurrency` | `2` | Parallel verification probes |
| `model_cache_ttl_seconds` | `300` | How long to cache the model list |
| `timeout_seconds` | `30` | Per-request timeout for LLM calls |
| `rate_limit_cooldown_seconds` | `60` | Cooldown after 429 (key-level) |
| `not_found_cooldown_seconds` | `3600` | Cooldown after 404 (model-level) |
| `auth_cooldown_seconds` | `600` | Cooldown after 401 auth error |
| `max_concurrent_requests` | `50` | Semaphore limit for non-streaming requests |
| `max_retries_per_request` | `3` | Max models attempted per request |
| `slow_request_threshold_ms` | `8000` | Log warning if response exceeds this |
| `score_cache_file` | `"score_cache.json"` | Filename for persisted scores |
| `metadata_weight_no_history` | `0.85` | Score for untested models (higher = tried sooner) |
| `metadata_weight_with_history` | `0.35` | Base score component for tested models |
| `top_model_pool_size` | `5` | Limit failover to top N ranked models |
| `min_model_attempts_for_confidence` | `8` | Blend toward no-history score until this many attempts |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `OPENROUTER_API_KEYS` | Comma-separated list of OpenRouter API keys |
| `SERVER_PORT` | Override listen port (default: 4141) |
| `SERVER_HOST` | Override listen host (default: 0.0.0.0) |

## Execution Flow

### Startup Sequence

```
main()
  ├── Parse flags (--debug, --port, --host)
  ├── logger.Init()
  ├── config.SetReloadHook(rebuildAdapters)
  ├── config.LoadEnv()          // Read .env file (strips quotes, no overwrite)
  ├── config.Load()             // Read config.json
  ├── met.Default = appMet      // Set package-level metrics singleton
  ├── Build cache directories
  ├── Load score cache from disk
  ├── Build shared HTTP client (connection pooling)
  ├── Build initial adapters (openrouter key pool + model router)
  ├── Build FailoverRouter with adapters, timeouts, cooldowns
  │
  ├── goroutine 1: cleanupExpiredMaps()       // Purge expired cooldowns every 5min
  ├── goroutine 2: RunToolVerification()      // Probe model tool support
  ├── goroutine 3: WatchReload()              // Listen for SIGHUP
  ├── goroutine 4: printModelTable()          // Wait for verification, then print
  ├── goroutine 5 (debug): re-print model table every 5 minutes
  │
  ├── api.New() → build mux with middleware
  ├── http.ListenAndServe()
  └── Wait for SIGINT/SIGTERM → graceful shutdown
```

### Request Lifecycle (Non-Streaming)

```
Client → POST /v1/chat/completions
  │
  ├── corsMiddleware()
  │   └── Set Access-Control-Allow-Origin: *
  │
  ├── loggingMiddleware()
  │   ├── Assign request ID (req-0001)
  │   ├── met.IncTotalRequests()
  │   ├── met.IncActiveRequests()
  │   └── defer met.DecActiveRequests()
  │
  ├── rateLimitMiddleware()
  │   ├── Skip for /v1/chat/completions (streams have own timeout)
  │   ├── Acquire semaphore slot for other endpoints
  │   └── Return 429 if at capacity
  │
  ├── handleChatCompletions()
  │   ├── Check method → 405 if not POST
  │   ├── Limit request body to 10MB
  │   ├── Decode JSON body
  │   ├── Determine requested model (specific / "auto")
  │   ├── Build modelsByProvider map:
  │   │   ├── "auto"         → getModelsByProvider() (all models)
  │   │   ├── "model-id"     → single-entry map (manual override)
  │   │   └── Filter through ToolRegistry for openrouter
  │   │
  │   ├── Check all models on cooldown → 503
  │   │   └── Skip check for manual override (failover handles it)
  │   │
  │   └── Route based on stream flag:
  │
  │       [NON-STREAM]
  │       └── AppFailover.ExecuteNonStream()
  │           ├── RankedModels() — sort by priority then score, truncate to top N
  │           ├── For each model (up to max_retries):
  │           │   ├── Skip if on cooldown
  │           │   ├── am.Adapter.ChatCompletion() (uses shared HTTP client)
  │           │   ├── On success → met.RecordSuccess(), return
  │           │   └── On error → HandleProviderError():
  │           │       ├── RateLimitError → record failure, continue
  │           │       ├── NotFoundError → MarkCooldown(), record failure, continue
  │           │       ├── AuthError → MarkCooldown(auth_cooldown_seconds), record failure, continue
  │           │       └── TimeoutError → record failure, continue
  │           └── All failed → return error
  │       │
  │       │   If manual override failed:
  │       │   └── Fall back to auto mode (free models ONLY, never paid)
  │       │
  │       [STREAM]
  │       └── Inline streaming loop
  │           ├── Same ranking and cooldown skip
  │           ├── am.Adapter.ChatCompletionStream()
  │           ├── Forward SSE chunks via chunkChan
  │           ├── On stream end:
  │           │   ├── sr.Err == nil → success
  │           │   ├── sentBytes → warn but return success (partial stream)
  │           │   └── else → HandleProviderError(), try next
  │           └── All failed → write error SSE + [DONE]
  │       │
  │       │   If manual override stream failed:
  │       │   └── Fall back to auto mode (free models ONLY)
  │
  └── Response sent to client
```

### Request Lifecycle (Key Pool)

```
TryAllKeys(persistent=true, model="gpt-4o-mini:free", fn)
  │
  ├── For each key in pool:
  │   ├── Skip if isKeyCooling(hint, model)
  │   ├── Call fn(key, hint, keyNum)
  │   │   └── Makes HTTP request to OpenRouter (shared connection pool)
  │   │
  │   ├── fn returns nil                           → success, return hint
  │   ├── fn returns ProviderError(RateLimitError) → markCooldown(), try next key
  │   └── fn returns other error                   → abort, return error
  │
  └── All keys exhausted → all-cooldown or last error
```

### Scoring System

```
ScoreModel(model):
  cfg = config.Get()

  If no history (success_rate < 0):
    return cfg.MetadataWeightNoHistory                    (default: 0.85)

  If total_attempts < cfg.MinModelAttemptsForConfidence:
    blend = total_attempts / MinModelAttemptsForConfidence
    return blend × (0.6×sr + 0.3×latScore + MetadataWeightWithHistory)
           + (1-blend) × MetadataWeightNoHistory

  Else:
    return 0.6 × success_rate + 0.3 × latency_score + MetadataWeightWithHistory

Where:
  success_rate   = successes / (successes + failures)
  latency_score  = max(0, 1 - avg_lat_ms / 30000)
  MetadataWeightNoHistory   = 0.85  (untested models)
  MetadataWeightWithHistory = 0.35  (tested models base)

GetPriority(model):
  - Checks model name against priority_keywords in config
  - Returns index of first matching keyword
  - Returns 999 if no match (lowest priority)

SortByScore(models):
  - Sorts by priority (lower index first), then by score (higher first)
  - Truncates to top_model_pool_size (default: 5)
  - Used by both ExecuteNonStream and streaming loop
```

### Health Check

```
GET /health
  │
  ├── Probe upstream: GET https://openrouter.ai/api/v1/models (5s timeout)
  │   ├── Success + 200 → upstreamOK = true
  │   └── Error or non-200 → upstreamOK = false
  │
  └── Response:
      ├── upstreamOK=true  → 200 {"status":"ok","upstream":true,...}
      └── upstreamOK=false → 503 {"status":"degraded","upstream":false,...}
```

### Configuration Reload

```
SIGHUP → WatchReload()
  ├── config.Load()
  │   └── Re-reads config.json, merges with defaults
  ├── reloadHook() → rebuildAdapters()
  │   ├── Build new OpenRouterAdapter (with shared HTTP client)
  │   ├── Update cloudAdapters (mutex-protected)
  │   └── Update failover.CloudAdapters (mutex-protected)
  └── Adapter list atomically replaced
```

### Graceful Shutdown

```
SIGINT/SIGTERM → main()
  ├── Cancel background context (stops verification, cleanup, periodic re-print)
  ├── server.Shutdown(shutdownTimeoutCtx)
  │   └── HTTP server stops accepting new connections
  │   └── Drains in-flight requests (waiting for handlers to complete)
  ├── toolRegistry.Save()
  ├── appMet.SaveScoreCache()
  └── Log final summary → exit
```

## Package Details

### `internal/api`

- **server.go**: `New()` factory — sets package globals (`AppMetrics`, `AppFailover`, `ToolRegistry`, `GetAdapters`, `SharedHTTPClient`), builds mux, wraps with `rateLimitMiddleware → corsMiddleware → loggingMiddleware`.
- **handlers.go**: Five handlers:
  - `handleHealth` — probes upstream OpenRouter connectivity, returns degraded/503 if unreachable
  - `handleMetrics` — per-model stats, key stats (numeric only, no hints leaked)
  - `handleCooldowns` — currently active model cooldowns
  - `handleModels` — all available models with scores
  - `handleChatCompletions` — non-streaming and streaming paths with manual override fallback
- **middleware.go**: `loggingMiddleware` — assigns request IDs, logs request/response, tracks active requests. `rateLimitMiddleware` — bounds concurrency via buffered channel semaphore, skips `/v1/chat/completions`. `corsMiddleware` — sets `Access-Control-Allow-Origin: *`. `statusWriter` — captures HTTP status code for logging, supports `Flush()` for SSE.

### `internal/config`

Singleton pattern. `Config` struct with `GlobalConfig` and per-provider `ProviderConfig`. `Load()` reads `config.json` and merges with defaults. `Get()` returns a copy under read lock. `WatchReload()` blocks on `SIGHUP` and calls `Load()` + `reloadHook`. `LoadEnv()` reads `.env`, strips surrounding quotes, and does not overwrite existing env vars.

### `internal/logger`

Mutex-guarded singleton with ANSI color escape codes. Levels: `INFO`, `WARN`, `ERROR`, `DEBUG`, plus `REQ` (request log) and `MODEL` (model status). `Banner()` renders a bordered table with per-column width calculation (accounts for invisible ANSI sequences).

### `internal/metrics`

`Metrics` struct holds `ModelStats` map, `KeyStats` map, and atomic counters. Package-level convenience functions delegate to a `Default` singleton set during startup. `Scorer` functions (`ScoreModel`, `GetPriority`, `SortByScore`, `HasHistory`) operate on the `Default` singleton. `TotalAttempts()` returns `Successes + Failures` for confidence blending.

### `internal/openrouter`

- **adapter.go**: `LLMAdapter` interface defines the contract for all providers. `OpenRouterAdapter` implements it with key-pool rotation, shared HTTP client (connection pooling), and SSE parsing (256KB scanner buffer). `ChatCompletionSingleKey` accepts `context.Context` for cancellation during verification probes.
- **keypool.go**: `KeyPool` manages a list of API keys with per-model cooldown tracking. `TryAllKeys` iterates keys, skipping cooled ones, persisting rate-limit cooldowns. `Next` picks the first non-cooled key. `CleanExpired` purges expired cooldown entries. `BuildHint` always prefixes with `…` for consistent masking.
- **models.go**: `ModelRouter` fetches `/models` from OpenRouter, filters to free models (`:free` suffix, zero pricing), excludes keyword-matched models, and caches with TTL.
- **registry.go**: `ToolSupportRegistry` persists model→tool-support booleans to a JSON file. Used to skip models that don't support tool calls.
- **verify.go**: Startup verification probes new models with a tool-call request. A model is marked as supporting tools if the API accepts the request without error (not just if it returns `tool_calls`). Timed-out models are cached as unsupported to avoid re-verification on every restart. Runs concurrently with configurable concurrency and timeout. Uses `context.Context` for cancellation.

### `internal/router`

`FailoverRouter` holds the adapter list, timeouts, per-model cooldown map, and ranking logic. `ExecuteNonStream` iterates ranked models, respecting cooldowns and retry budget. `RankedModels` combines adapters with their model lists, sorts by priority + score, and truncates to `top_model_pool_size`. `HandleProviderError` classifies errors into rate-limit, not-found, auth, timeout, and generic; applies configurable cooldowns for not-found and auth errors.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check with upstream probe |
| GET | `/metrics` | Per-model and per-key statistics |
| GET | `/cooldowns` | Currently active model cooldowns |
| GET | `/v1/models` | List all available models with scores |
| POST | `/v1/chat/completions` | OpenAI-compatible chat completions (stream + non-stream) |
| OPTIONS | `*` | CORS preflight (204 No Content) |

## Request Flow Diagram

```
                    ┌─────────────────────────────────────────┐
                    │              Client Request              │
                    └──────────────────┬──────────────────────┘
                                       │
                    ┌──────────────────▼──────────────────────┐
                    │         CORS Middleware                  │
                    │    (Access-Control-Allow-Origin: *)      │
                    └──────────────────┬──────────────────────┘
                                       │
                    ┌──────────────────▼──────────────────────┐
                    │       Logging Middleware                 │
                    │  (request ID, timing, status code)      │
                    └──────────────────┬──────────────────────┘
                                       │
                    ┌──────────────────▼──────────────────────┐
                    │     Rate Limit Middleware                │
                    │  (skip for /v1/chat/completions)        │
                    │  (429 if at capacity)                   │
                    └──────────────────┬──────────────────────┘
                                       │
              ┌────────────────────────┼────────────────────────┐
              │                        │                        │
    ┌─────────▼─────────┐  ┌──────────▼──────────┐  ┌─────────▼─────────┐
    │   /health         │  │  /v1/models         │  │ /v1/chat/         │
    │   Probe upstream  │  │  List + scores      │  │   completions     │
    └───────────────────┘  └─────────────────────┘  └─────────┬─────────┘
                                                              │
                                               ┌──────────────▼──────────────┐
                                               │   Manual Override?          │
                                               │   Yes → Try specific model  │
                                               │   No  → Auto (all models)   │
                                               └──────────────┬──────────────┘
                                                              │
                                               ┌──────────────▼──────────────┐
                                               │   FailoverRouter            │
                                               │   RankedModels (top N)      │
                                               │   For each model:           │
                                               │     TryAllKeys → OpenRouter │
                                               │     On error: cooldown      │
                                               │     On success: return      │
                                               └──────────────┬──────────────┘
                                                              │
                                               ┌──────────────▼──────────────┐
                                               │   Manual override failed?   │
                                               │   Yes → Fall back to auto   │
                                               │   (free models only)        │
                                               └──────────────┬──────────────┘
                                                              │
                                               ┌──────────────▼──────────────┐
                                               │   Response to client        │
                                               └─────────────────────────────┘
```
