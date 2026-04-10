#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PID_DIR="${ROOT_DIR}/.local/raft/pids"

stop_pid_file() {
  local pid_file="$1"
  local node_name
  node_name="$(basename "$pid_file" .pid)"

  if [[ ! -f "$pid_file" ]]; then
    return
  fi

  local pid
  pid="$(cat "$pid_file")"
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid"
    echo "Stopped ${node_name} pid=${pid}"
  else
    echo "${node_name} was not running"
  fi
  rm -f "$pid_file"
}

if [[ ! -d "$PID_DIR" ]]; then
  echo "No local Raft PID directory found"
  exit 0
fi

stop_pid_file "${PID_DIR}/node-2.pid"
stop_pid_file "${PID_DIR}/node-1.pid"
