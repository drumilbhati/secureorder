#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

STATE_DIR="${ROOT_DIR}/.local/evm"
LOG_DIR="${STATE_DIR}/logs"
PID_DIR="${STATE_DIR}/pids"

mkdir -p "$LOG_DIR" "$PID_DIR"

if [[ ! -d node_modules ]]; then
  echo "node_modules not found. Running npm install first."
  npm install
fi

PID_FILE="${PID_DIR}/hardhat.pid"
LOG_FILE="${LOG_DIR}/hardhat-node.log"

if [[ -f "$PID_FILE" ]]; then
  existing_pid="$(cat "$PID_FILE")"
  if kill -0 "$existing_pid" >/dev/null 2>&1; then
    echo "Hardhat node already running with pid ${existing_pid}"
    exit 0
  fi
  rm -f "$PID_FILE"
fi

nohup npx hardhat node >"$LOG_FILE" 2>&1 &
pid=$!
echo "$pid" >"$PID_FILE"

echo "Started local Hardhat node pid=${pid}"
echo "Log: ${LOG_FILE}"
echo "RPC: http://127.0.0.1:8545"
