#!/usr/bin/env bash
set -euo pipefail

BINARY="${1:-free-model-router}"

echo "Building $BINARY…"
go build -o "$BINARY" ./cmd/freemodel/
echo "Done: ./$BINARY"
