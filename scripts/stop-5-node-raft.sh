#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_DIR="${ROOT_DIR}/.local/raft-5/pids"

if [[ ! -d "$PID_DIR" ]]; then
  echo "No PID directory found at $PID_DIR"
  exit 0
fi

for pid_file in "${PID_DIR}"/*.pid; do
  if [[ -f "$pid_file" ]]; then
    node_id=$(basename "$pid_file" .pid)
    pid=$(cat "$pid_file")
    if kill -0 "$pid" >/dev/null 2>&1; then
      echo "Stopping ${node_id} (pid ${pid})..."
      kill "$pid"
    else
      echo "${node_id} (pid ${pid}) is not running."
    fi
    rm -f "$pid_file"
  fi
done

echo "All nodes stopped."
