#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PID_FILE="${ROOT_DIR}/.local/evm/pids/hardhat.pid"

if [[ ! -f "$PID_FILE" ]]; then
  echo "Local Hardhat node is not running"
  exit 0
fi

pid="$(cat "$PID_FILE")"
if kill -0 "$pid" >/dev/null 2>&1; then
  kill "$pid"
  wait "$pid" 2>/dev/null || true
  echo "Stopped local Hardhat node pid=${pid}"
else
  echo "Local Hardhat node was not running"
fi

rm -f "$PID_FILE"
