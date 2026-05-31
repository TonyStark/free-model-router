# Contributing

Thanks for your interest in contributing to Free Model Router.

## Getting Started

1. Fork the repository.
2. Clone your fork:
   ```bash
   git clone https://github.com/<your-username>/free-model-router.git
   ```
3. Create a feature branch:
   ```bash
   git checkout -b feat/my-feature
   ```

## Development Workflow

```bash
# Build
make build

# Run tests
make test

# Run with race detector
make test-race

# Run coverage
make test-cover

# Lint and vet
make lint
make vet
```

## Code Style

- Follow [effective Go](https://go.dev/doc/effective_go) conventions.
- Use `gofmt` (or `go fmt`) before committing — no trailing whitespace, tabs for indentation.
- Run `go vet ./...` and `staticcheck ./...` — both must pass.
- Zero compiler warnings. No unused variables, imports, or dead code.
- Exported symbols must have doc comments.

## Commit Conventions

Use conventional commit messages:

- `feat:` — new feature
- `fix:` — bug fix
- `refactor:` — code restructuring
- `test:` — adding or updating tests
- `docs:` — documentation changes
- `chore:` — build, CI, tooling

Keep commits atomic (one logical change per commit).

## Testing Guidelines

- **Unit tests** for all new pure logic (metrics, scoring, key pool logic).
- **Table-driven tests** preferred for functions with multiple cases.
- **Mock interfaces** for external dependencies (HTTP calls, filesystem).
- **Test temp files** (via `t.TempDir()`) for filesystem-dependent tests.
- New code must maintain or improve overall coverage.

### Running all tests

```bash
go test -count=1 -cover ./...
```

## Pull Request Process

1. Run `make all` locally — build, vet, lint, and test must all pass.
2. Push your branch and open a pull request against `main`.
3. Keep PRs focused on a single concern. Split large changes into multiple PRs.
4. The CI pipeline runs on all PRs. Ensure the build badge is green.

## Adding a New Provider

1. Implement the `openrouter.LLMAdapter` interface (see `adapter.go`).
2. Add provider config section in `config.json`.
3. Register the provider in `buildOpenRouterAdapter` / `rebuildAdapters` in `main.go`.
4. Add the provider name to `enabled_providers` in `config.json`.
5. Add tests for the new adapter.

## Reporting Issues

- Search existing issues before opening a new one.
- Include: Go version, OS, config (sanitized), logs, and steps to reproduce.
- Label feature requests with `enhancement` and bugs with `bug`.

## Code of Conduct

Be respectful and constructive. Harassment, discrimination, or toxic behavior will not be tolerated.
