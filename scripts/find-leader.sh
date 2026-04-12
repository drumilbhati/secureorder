#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${ROOT_DIR}/.local/raft-5/logs"

if [[ ! -d "$LOG_DIR" ]]; then
  echo "No log directory found at $LOG_DIR"
  exit 1
fi

for log_file in "${LOG_DIR}"/*.log; do
  node_id=$(basename "$log_file" .log)
  if grep -q "entering Leader state" "$log_file"; then
    # Get the latest leadership entry
    if [[ $(grep "entering Leader state" "$log_file" | tail -1) ]]; then
        # Check if it didn't leave leader state since then
        last_leader_entry=$(grep -n "entering Leader state" "$log_file" | tail -1 | cut -d: -f1)
        last_follower_entry=$(grep -n "entering Follower state" "$log_file" | tail -1 | cut -d: -f1 || echo 0)
        last_candidate_entry=$(grep -n "entering Candidate state" "$log_file" | tail -1 | cut -d: -f1 || echo 0)

        if [[ $last_leader_entry -gt $last_follower_entry ]] && [[ $last_leader_entry -gt $last_candidate_entry ]]; then
            grpc_port=$(grep "gRPC bind" "$log_file" | head -1 | awk -F: '{print $NF}')
            echo "LEADER: ${node_id} (gRPC on port ${grpc_port})"
            exit 0
        fi
    fi
  fi
done

echo "Leader not found in logs. The cluster might still be electing or logs have rotated."
