# Free Model Router

A production-grade Go HTTP server that routes chat completion requests to free OpenRouter models with intelligent failover, rate-limit management, and per-model scoring.

## Features

- **Multi-key rotation** — Distributes requests across multiple API keys; falls through on rate limits
- **Intelligent failover** — Ranks models by success rate and latency; skips cooling-down models
- **Config-driven scoring** — Blended score formula with tunable weights and confidence blending for new models
- **Model cooldowns** — Per-model cooldowns on auth/not-found/rate-limit errors with configurable durations
- **Auth error cooldown** — Auto-cools models when API key authentication fails (`auth_cooldown_seconds`)
- **Tool support verification** — Probes new models with a tool call to build a persisted support cache
- **Graceful shutdown** — Drains active requests, persists score/tool caches, then exits
- **Hot-reload** — `SIGHUP` reloads `config.json` and rebuilds adapters without downtime
- **Streaming** — SSE streaming with per-chunk forwarding and stream-level failover
- **Comprehensive metrics** — Per-model success/failure/latency, per-key stats, and active-request tracking
- **Rate-limit middleware** — Enforces `max_concurrent_requests` with HTTP 429 responses (skips streaming)
- **CORS middleware** — Configurable cross-origin request handling
- **Health check** — Upstream connectivity probe with degraded status fallback
- **Request body limit** — 10MB max request body via `MaxBytesReader`
- **Connection pooling** — Shared HTTP client with 100 max connections, 90s idle timeout

## Architecture

```
cmd/freemodel/main.go       # Entrypoint: wiring, signal handling, adapter building
internal/
├── api/                    # HTTP server, handlers, middleware
├── config/                 # JSON config loading, env parsing, hot-reload
├── logger/                 # ANSI-colored structured logger
├── metrics/                # Per-model and per-key metrics, scoring
├── openrouter/             # OpenRouter adapter, key pool, model registry, tool verification
└── router/                 # Failover router with cooldown management
```

Full architecture documentation: [docs/architecture.md](docs/architecture.md)

## Prerequisites

- Go 1.21+
- OpenRouter API key(s) with free model access

## Installation

```bash
git clone <repo-url> free-model-router
cd free-model-router
go build -o free-model-router ./cmd/freemodel/
```

## Configuration

### `config.json`

| Field | Type | Default | Description |
|---|---|---|---|
| `global.timeout_seconds` | float | `30` | HTTP request timeout per provider call |
| `global.rate_limit_cooldown_seconds` | float | `60` | Cooldown duration after a 429 response |
| `global.not_found_cooldown_seconds` | float | `3600` | Cooldown duration after a 404 |
| `global.auth_cooldown_seconds` | float | `600` | Cooldown duration after an authentication error |
| `global.max_concurrent_requests` | int | `50` | Max simultaneous requests (429 beyond this) |
| `global.max_retries_per_request` | int | `3` | Max model attempts per user request |
| `global.model_cache_ttl_seconds` | int | `300` | TTL for the model list fetched from OpenRouter |
| `global.verify_tool_support` | bool | `true` | Enable tool-call probing at startup |
| `global.verify_timeout_seconds` | float | `30` | Per-model probe timeout |
| `global.verify_concurrency` | int | `2` | Max concurrent probe requests |
| `global.slow_request_threshold_ms` | int | `8000` | Log a warning if a request exceeds this |
| `global.score_cache_file` | string | `"score_cache.json"` | File name inside `cache_dir` |
| `global.shutdown_timeout_seconds` | int | `20` | Graceful shutdown drain timeout |
| `global.cache_dir` | string | `".cache"` | Directory for cache files |
| `global.metadata_weight_no_history` | float | `0.85` | Base score for untested models (no history) |
| `global.metadata_weight_with_history` | float | `0.35` | Base score component for models with history |
| `global.top_model_pool_size` | int | `5` | Max models to consider per request (0 = unlimited) |
| `global.min_model_attempts_for_confidence` | int | `8` | Attempts before full confidence in score (blending below this) |
| `enabled_providers` | [string] | `["openrouter"]` | Providers to activate |
| `providers.<name>.base_url` | string | — | Base URL for the provider API |
| `providers.<name>.priority_keywords` | [string] | — | Models matching these get priority routing |
| `providers.<name>.exclude_keywords` | [string] | — | Models matching these are excluded |

See `config.json` for a complete example with all supported providers.

### `.env` / Environment Variables

| Variable | Description |
|---|---|
| `OPENROUTER_API_KEYS` | Comma-separated list of API keys (required) |
| `SERVER_PORT` | HTTP port (default `4141`) |
| `SERVER_HOST` | Bind address (default `0.0.0.0`) |

Keys can also be set via a `.env` file in the working directory.

## Build

```bash
go build -o free-model-router ./cmd/freemodel/
```

Cross-compile:

```bash
GOOS=linux GOARCH=amd64 go build -o free-model-router-linux-amd64 ./cmd/freemodel/
GOOS=darwin GOARCH=arm64 go build -o free-model-router-darwin-arm64 ./cmd/freemodel/
```

## Run

```bash
# Quick start (reads config.json and .env from current directory)
./free-model-router

# With debug logging
./free-model-router -debug

# Override port/host
./free-model-router -port 8080 -host 127.0.0.1
```

## API Endpoints

### `GET /health`

```json
{"status":"ok","metrics":"total=0 ok=0 err=0 active=0 streams=0","time":"2026-05-31T16:00:00Z"}
```

### `GET /metrics`

Per-model and per-key statistics.

### `GET /cooldowns`

```json
{"model-name":{"expires_in_seconds":42}}
```

### `GET /v1/models`

OpenAI-compatible model listing. Models are sorted by score descending. Returns `auto`, `free-model-router/auto`, and all fetched models.

### `POST /v1/chat/completions`

OpenAI-compatible chat completions endpoint.

**Request:**

```json
{
  "model": "auto",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": false
}
```

**Response (non-streaming):**

Direct passthrough of the upstream provider's response.

**Streaming (`stream: true`):**

SSE stream of chunks forwarded from the upstream provider. On error, falls through to the next model mid-stream.

Supported model values:
- `"auto"` or `"free-model-router/auto"` — Route across all available models
- `"model-id"` — Use exactly that model (no failover to different IDs)
- `"openrouter/model-id"` — OpenRouter-prefixed identifier

## Examples

```bash
# Health check
curl http://localhost:4141/health

# List models
curl http://localhost:4141/v1/models

# Chat completion
curl -X POST http://localhost:4141/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"Hello"}],"stream":false}'

# Streaming
curl -N -X POST http://localhost:4141/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"Write a poem"}],"stream":true}'

# View per-model cooldowns
curl http://localhost:4141/cooldowns

# View metrics
curl http://localhost:4141/metrics
```

## Operations

### Hot-reload

Send `SIGHUP` to reload `config.json` and rebuild adapters:

```bash
kill -HUP <pid>
```

### Graceful shutdown

Send `SIGINT` or `SIGTERM`:

```bash
kill -TERM <pid>
```

The server drains active requests (up to `shutdown_timeout_seconds`), persists the score cache and tool-support cache, logs a summary, then exits.

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| Server won't start | Port in use | Change port via `-port` or `SERVER_PORT` |
| `no API keys configured` | `OPENROUTER_API_KEYS` missing | Set in `.env` or environment |
| `RateLimitError` | API key exhausted | Add more keys or increase `rate_limit_cooldown_seconds` |
| `all models on cooldown` | All models recently errored | Wait for cooldowns to expire, or clear cooldowns at `/cooldowns` |
| Empty model list | Network issue or provider down | Check `config.json` `base_url` and connectivity to OpenRouter |

## Development

### Running tests

```bash
go test ./...
go test -cover ./...
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

### Code quality

```bash
go vet ./...
staticcheck ./...
```

### Test structure

- **Unit tests** — `internal/metrics`, `internal/config`, `internal/logger`
- **Table-driven tests** — `internal/openrouter/keypool_test.go`, `internal/metrics/scorer_test.go`
- **Mock-based tests** — `internal/router/router_test.go`, `internal/api/handlers_test.go`
- **Fixture-based tests** — `internal/config/config_test.go` (temp files), `internal/openrouter/registry_test.go` (temp files)

## License

MIT

## Project Status

Production-ready. Under active development.
