#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

STATE_DIR="${ROOT_DIR}/.local/raft"
LOG_DIR="${STATE_DIR}/logs"
PID_DIR="${STATE_DIR}/pids"
DATA_DIR="${STATE_DIR}/data"

PEERS="node-1=127.0.0.1:7000,node-2=127.0.0.1:7001"

mkdir -p "$LOG_DIR" "$PID_DIR" "$DATA_DIR"

if [[ "${1:-}" == "--rebuild" ]]; then
  "${ROOT_DIR}/scripts/build-local.sh"
fi

if [[ "${1:-}" == "--fresh" ]] || [[ "${2:-}" == "--fresh" ]]; then
  "${ROOT_DIR}/scripts/stop-local-raft.sh"
  rm -rf "${DATA_DIR}/node-1" "${DATA_DIR}/node-2"
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

  nohup "${args[@]}" >"$log_file" 2>&1 &
  local pid=$!
  echo "$pid" >"$pid_file"
  echo "Started ${node_id} pid=${pid} grpc=${grpc_addr} raft=${raft_bind}"
}

start_node node-1 :12345 127.0.0.1:7000 true
sleep 1
start_node node-2 :12346 127.0.0.1:7001 false

echo
echo "Cluster state:"
echo "  Logs: ${LOG_DIR}"
echo "  PIDs: ${PID_DIR}"
echo "  Node 1 gRPC: localhost:12345"
echo "  Node 2 gRPC: localhost:12346"
echo
echo "Useful commands:"
echo "  tail -f ${LOG_DIR}/node-1.log"
echo "  tail -f ${LOG_DIR}/node-2.log"
echo "  ${ROOT_DIR}/scripts/load-local-raft.sh"
echo "  ${ROOT_DIR}/scripts/stop-local-raft.sh"
