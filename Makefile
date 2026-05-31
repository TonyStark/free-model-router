BINARY   ?= free-model-router
GO       ?= go
GOFLAGS  ?=

.PHONY: all build test lint vet clean run cross-build help

all: lint vet test build

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/freemodel/

run: build
	./$(BINARY)

test:
	$(GO) test $(GOFLAGS) -count=1 ./...

test-race:
	$(GO) test $(GOFLAGS) -race -count=1 ./...

test-cover:
	$(GO) test $(GOFLAGS) -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html

vet:
	$(GO) vet $(GOFLAGS) ./...

lint:
	@which staticcheck > /dev/null 2>&1 || (echo "Installing staticcheck…"; go install honnef.co/go/tools/cmd/staticcheck@latest)
	staticcheck $(GOFLAGS) ./...

clean:
	rm -f $(BINARY) $(BINARY)-* coverage.out coverage.html
	rm -rf .cache/

cross-build:
	GOOS=linux   GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)-linux-amd64   ./cmd/freemodel/
	GOOS=linux   GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)-linux-arm64   ./cmd/freemodel/
	GOOS=darwin  GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)-darwin-amd64  ./cmd/freemodel/
	GOOS=darwin  GOARCH=arm64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)-darwin-arm64  ./cmd/freemodel/
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY)-windows-amd64.exe ./cmd/freemodel/

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  all          Build, lint, vet, and test"
	@echo "  build        Build the binary"
	@echo "  run          Build and run"
	@echo "  test         Run unit tests"
	@echo "  test-race    Run tests with race detector"
	@echo "  test-cover   Run tests with coverage report"
	@echo "  vet          Run go vet"
	@echo "  lint         Run staticcheck"
	@echo "  clean        Remove build artifacts"
	@echo "  cross-build  Cross-compile for all platforms"
