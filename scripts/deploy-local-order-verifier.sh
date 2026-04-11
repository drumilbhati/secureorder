#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -d node_modules ]]; then
  echo "node_modules not found. Running npm install first."
  npm install
fi

deploy_output="$(npx hardhat run scripts/deploy-order-verifier.ts --network hardhatMainnet)"
echo "$deploy_output"

address="$(echo "$deploy_output" | awk '/OrderVerifier deployed at:/ {print $4}')"
if [[ -z "$address" ]]; then
  echo "Failed to extract deployed contract address" >&2
  exit 1
fi

mkdir -p .local/evm
printf '%s\n' "$address" > .local/evm/order_verifier.address

echo "Saved deployed address to .local/evm/order_verifier.address"
