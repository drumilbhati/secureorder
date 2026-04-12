#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

STATE_DIR="${ROOT_DIR}/.local/raft-5"
LOG_DIR="${STATE_DIR}/logs"
PID_DIR="${STATE_DIR}/pids"
DATA_DIR="${STATE_DIR}/data"

PEERS="node-1=127.0.0.1:7000,node-2=127.0.0.1:7001,node-3=127.0.0.1:7002,node-4=127.0.0.1:7003,node-5=127.0.0.1:7004"

mkdir -p "$LOG_DIR" "$PID_DIR" "$DATA_DIR"

if [[ "${1:-}" == "--rebuild" ]]; then
  "${ROOT_DIR}/scripts/build-local.sh"
fi

if [[ "${1:-}" == "--fresh" ]] || [[ "${2:-}" == "--fresh" ]]; then
  # Stop existing processes first
  for pid_file in "${PID_DIR}"/*.pid; do
    if [[ -f "$pid_file" ]]; then
      pid=$(cat "$pid_file")
      kill "$pid" >/dev/null 2>&1 || true
      rm -f "$pid_file"
    fi
  done
  rm -rf "${DATA_DIR}"/*
fi

if [[ ! -x "${ROOT_DIR}/bin/sequencer" ]]; then
  echo "bin/sequencer not found. Building binaries first."
  "${ROOT_DIR}/scripts/build-local.sh"
fi

start_node() {
  local node_id="$1"
  local grpc_addr="$2"
  local raft_bind="$3"
  local bootstrap="$4"
  local log_file="${LOG_DIR}/${node_id}.log"
  local pid_file="${PID_DIR}/${node_id}.pid"
  local node_data_dir="${DATA_DIR}/${node_id}"

  if [[ -f "$pid_file" ]]; then
    local existing_pid
    existing_pid="$(cat "$pid_file")"
    if kill -0 "$existing_pid" >/dev/null 2>&1; then
      echo "${node_id} is already running with pid ${existing_pid}"
      return
    fi
    rm -f "$pid_file"
  fi

  local args=(
    "${ROOT_DIR}/bin/sequencer"
    -ordering=raft
    "-grpc-addr=${grpc_addr}"
    "-raft-node-id=${node_id}"
    "-raft-bind=${raft_bind}"
    "-raft-peers=${PEERS}"
    "-raft-data-dir=${node_data_dir}"
  )

  if [[ "$bootstrap" == "true" ]]; then
    args+=(-raft-bootstrap)
  fi

  # Make sure we export LD_LIBRARY_PATH for the sequencer
  export LD_LIBRARY_PATH="${ROOT_DIR}/cpp/build/lib:${LD_LIBRARY_PATH:-}"

  nohup "${args[@]}" >"$log_file" 2>&1 &
  local pid=$!
  echo "$pid" >"$pid_file"
  echo "Started ${node_id} pid=${pid} grpc=${grpc_addr} raft=${raft_bind}"
}

echo "Starting 5-node sequencer cluster..."
start_node node-1 :12345 127.0.0.1:7000 true
sleep 1
start_node node-2 :12346 127.0.0.1:7001 false
start_node node-3 :12347 127.0.0.1:7002 false
start_node node-4 :12348 127.0.0.1:7003 false
start_node node-5 :12349 127.0.0.1:7004 false

echo
echo "Cluster state:"
echo "  Logs: ${LOG_DIR}"
echo "  PIDs: ${PID_DIR}"
echo "  Node 1 gRPC: localhost:12345"
echo "  Node 2 gRPC: localhost:12346"
echo "  Node 3 gRPC: localhost:12347"
echo "  Node 4 gRPC: localhost:12348"
echo "  Node 5 gRPC: localhost:12349"
echo
echo "Useful commands:"
echo "  tail -f ${LOG_DIR}/node-1.log"
echo "  ./scripts/stop-5-node-raft.sh"
