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
│   │   └── middleware.go        # Logging, rate-limit, request ID
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

## Execution Flow

### Startup Sequence

```
main()
  ├── Parse flags (--debug, --port, --host)
  ├── logger.Init()
  ├── config.SetReloadHook(rebuildAdapters)
  ├── config.LoadEnv()          // Read .env file
  ├── config.Load()             // Read config.json
  ├── met.Default = appMet      // Set package-level metrics singleton
  ├── Build cache directories
  ├── Load score cache from disk
  ├── Build initial adapters (openrouter key pool + model router)
  ├── Build FailoverRouter with adapters, timeouts, cooldowns
  │
  ├── goroutine 1: RunToolVerification()          // Probe model tool support
  ├── goroutine 2: WatchReload()                  // Listen for SIGHUP
  ├── goroutine 3: printModelTable()              // Wait for verification, then print
  ├── goroutine 4 (debug): re-print model table every 5 minutes
  │
  ├── api.New() → build mux with middleware
  ├── http.ListenAndServe()
  └── Wait for SIGINT/SIGTERM → graceful shutdown
```

### Request Lifecycle (Non-Streaming)

```
Client → POST /v1/chat/completions
  │
  ├── loggingMiddleware()
  │   ├── Assign request ID (req-0001)
  │   ├── met.IncTotalRequests()
  │   ├── met.IncActiveRequests()
  │   └── defer met.DecActiveRequests()
  │
  ├── rateLimitMiddleware()
  │   ├── Acquire semaphore slot
  │   └── Return 429 if at capacity
  │
  ├── handleChatCompletions()
  │   ├── Decode JSON body
  │   ├── Determine requested model (specific / "auto")
  │   ├── Build modelsByProvider map:
  │   │   ├── "auto"         → getModelsByProvider() (all models)
  │   │   ├── "model-id"     → single-entry map
  │   │   └── Filter through ToolRegistry for openrouter
  │   │
  │   ├── Check all models on cooldown → 503
  │   │
  │   └── Route based on stream flag:
  │
  │       [NON-STREAM]
  │       └── AppFailover.ExecuteNonStream()
  │           ├── RankedModels() — sort by priority then score
  │           ├── For each model (up to max_retries):
  │           │   ├── Skip if on cooldown
  │           │   ├── am.Adapter.ChatCompletion()
  │           │   ├── On success → met.RecordSuccess(), return
  │           │   └── On error → HandleProviderError():
  │           │       ├── RateLimitError → record failure, continue
  │           │       ├── NotFoundError → MarkCooldown(), record failure, continue
  │           │       ├── AuthError    → record failure, continue
  │           │       └── TimeoutError → record failure, continue
  │           └── All failed → return error
  │
  │       [STREAM]
  │       └── Inline streaming loop
  │           ├── Same ranking and cooldown skip
  │           ├── am.Adapter.ChatCompletionStream()
  │           ├── Forward SSE chunks via chunkChan
  │           ├── On stream end:
  │           │   ├── sr.Err == nil || sentBytes → success
  │           │   └── else → HandleProviderError(), try next
  │           └── All failed → write error SSE + [DONE]
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
  │   │   └── Makes HTTP request to OpenRouter
  │   │
  │   ├── fn returns nil                           → success, return hint
  │   ├── fn returns ProviderError(RateLimitError) → markCooldown(), try next key
  │   └── fn returns other error                   → abort, return error
  │
  └── All keys exhausted → all-cooldown or last error
```

### Scoring System

```
ScoreModel(model) = 0.6 × success_rate + 0.3 × latency_score + 0.1

Where:
  success_rate   = successes / (successes + failures)   — or -1 if no history
  latency_score  = max(0, 1 - avg_lat_ms / 30000)       — normalized latency
  base_bonus     = 0.1

Without history:
  ScoreModel(model) = 0.5 + 0.3 × latency_score

GetPriority(model):
  - Checks model name against priority_keywords in config
  - Returns index of first matching keyword
  - Returns 999 if no match (lowest priority)

SortByScore(models):
  - Sorts by priority (lower index first), then by score (higher first)
  - Used by both ExecuteNonStream and streaming loop
```

### Configuration Reload

```
SIGHUP → WatchReload()
  ├── config.Load()
  │   └── Re-reads config.json, merges with defaults
  ├── reloadHook() → rebuildAdapters()
  │   ├── Build new OpenRouterAdapter
  │   ├── Update cloudAdapters (mutex-protected)
  │   └── Update failover.CloudAdapters (mutex-protected)
  └── Adapter list atomically replaced
```

### Graceful Shutdown

```
SIGINT/SIGTERM → main()
  ├── Cancel background context (stops verification, periodic re-print)
  ├── server.Shutdown(shutdownTimeoutCtx)
  │   └── HTTP server stops accepting new connections
  │   └── Drains in-flight requests (waiting for handlers to complete)
  ├── toolRegistry.Save()
  ├── appMet.SaveScoreCache()
  └── Log final summary → exit
```

## Package Details

### `internal/api`

- **server.go**: `New()` factory — sets package globals (`AppMetrics`, `AppFailover`, `ToolRegistry`, `GetAdapters`), builds mux, wraps with `loggingMiddleware` and `rateLimitMiddleware`.
- **handlers.go**: Five handlers — `handleHealth`, `handleMetrics`, `handleCooldowns`, `handleModels`, `handleChatCompletions`. The chat completions handler contains both non-streaming and streaming paths.
- **middleware.go**: `loggingMiddleware` — assigns request IDs, logs request/response, tracks active requests. `rateLimitMiddleware` — bounds concurrency via a buffered channel semaphore. `statusWriter` — captures HTTP status code for logging.

### `internal/config`

Singleton pattern. `Config` struct with `GlobalConfig` and per-provider `ProviderConfig`. `Load()` reads `config.json` and merges with defaults. `Get()` returns a copy under read lock. `WatchReload()` blocks on `SIGHUP` and calls `Load()` + `reloadHook`.

### `internal/logger`

Mutex-guarded singleton with ANSI color escape codes. Levels: `INFO`, `WARN`, `ERROR`, `DEBUG`, plus `REQ` (request log) and `MODEL` (model status). `Banner()` renders a bordered table with per-column width calculation (accounts for invisible ANSI sequences).

### `internal/metrics`

`Metrics` struct holds `ModelStats` map, `KeyStats` map, and atomic counters. Package-level convenience functions delegate to a `Default` singleton set during startup. `Scorer` functions (`ScoreModel`, `GetPriority`, `SortByScore`, `HasHistory`) operate on the `Default` singleton.

### `internal/openrouter`

- **adapter.go**: `LLMAdapter` interface defines the contract for all providers. `OpenRouterAdapter` implements it with key-pool rotation, HTTP calls, and SSE parsing.
- **keypool.go**: `KeyPool` manages a list of API keys with per-model cooldown tracking. `TryAllKeys` iterates keys, skipping cooled ones, persisting rate-limit cooldowns. `Next` picks the first non-cooled key.
- **models.go**: `ModelRouter` fetches `/models` from OpenRouter, filters to free models (`:free` suffix, zero pricing), excludes keyword-matched models, and caches with TTL.
- **registry.go**: `ToolSupportRegistry` persists model→tool-support booleans to a JSON file. Used to skip models that don't support tool calls.
- **verify.go**: Startup verification probes new models with a tool-call request to determine tool support. Runs concurrently with configurable concurrency and timeout.

### `internal/router`

`FailoverRouter` holds the adapter list, timeouts, per-model cooldown map, and ranking logic. `ExecuteNonStream` iterates ranked models, respecting cooldowns and retry budget. `RankedModels` combines adapters with their model lists and sorts by priority + score. `HandleProviderError` classifies errors into rate-limit, not-found, auth, timeout, and generic; applies cooldowns for not-found errors.
