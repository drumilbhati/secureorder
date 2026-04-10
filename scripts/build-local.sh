#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

mkdir -p bin

echo "Building local binaries..."
go build -o bin/sequencer ./cmd/sequencer
go build -o bin/client ./cmd/client
go build -o bin/rpc-loadtest ./cmd/rpc-loadtest

echo "Built:"
echo "  bin/sequencer"
echo "  bin/client"
echo "  bin/rpc-loadtest"
