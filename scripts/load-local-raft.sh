#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ADDR="${ADDR:-localhost:12345}"
PUBKEY="${PUBKEY:-keys/sequencer_public.key}"
CLIENTS="${CLIENTS:-50}"
REQUESTS="${REQUESTS:-10}"
TIMEOUT="${TIMEOUT:-30s}"

if [[ ! -x "${ROOT_DIR}/bin/rpc-loadtest" ]]; then
  echo "bin/rpc-loadtest not found. Building binaries first."
  "${ROOT_DIR}/scripts/build-local.sh"
fi

echo "Running encrypted load test"
echo "  addr=${ADDR}"
echo "  pubkey=${PUBKEY}"
echo "  clients=${CLIENTS}"
echo "  requests=${REQUESTS}"
echo "  timeout=${TIMEOUT}"

"${ROOT_DIR}/bin/rpc-loadtest" \
  -addr="${ADDR}" \
  -pubkey="${PUBKEY}" \
  -clients="${CLIENTS}" \
  -requests="${REQUESTS}" \
  -timeout="${TIMEOUT}"
