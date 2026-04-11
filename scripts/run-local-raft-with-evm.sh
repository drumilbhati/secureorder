#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RPC_URL="${ORDER_VERIFIER_RPC_URL:-http://127.0.0.1:8545}"
CHAIN_ID="${ORDER_VERIFIER_CHAIN_ID:-31337}"
PRIVATE_KEY="${ORDER_VERIFIER_PRIVATE_KEY:-ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80}"
ADDRESS_FILE="${ROOT_DIR}/.local/evm/order_verifier.address"

if [[ ! -f "$ADDRESS_FILE" ]]; then
  echo "Missing ${ADDRESS_FILE}. Deploy OrderVerifier first:"
  echo "  ./scripts/run-local-evm.sh"
  echo "  ./scripts/deploy-local-order-verifier.sh"
  exit 1
fi

ORDER_VERIFIER_CONTRACT="$(cat "$ADDRESS_FILE")"

echo "Starting local Raft cluster with EVM commitment publishing"
echo "  rpc_url=${RPC_URL}"
echo "  contract=${ORDER_VERIFIER_CONTRACT}"
echo "  chain_id=${CHAIN_ID}"

export ORDER_VERIFIER_RPC_URL="${RPC_URL}"
export ORDER_VERIFIER_CONTRACT="${ORDER_VERIFIER_CONTRACT}"
export ORDER_VERIFIER_PRIVATE_KEY="${PRIVATE_KEY}"
export ORDER_VERIFIER_CHAIN_ID="${CHAIN_ID}"

./scripts/run-local-raft.sh "${@}"
