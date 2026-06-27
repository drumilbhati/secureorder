package settlement

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
)

// NewPublisherFromEnv constructs the appropriate CommitmentPublisher from
// environment variables. It provides a safe, zero-config default (NoopPublisher)
// when the EVM environment is not configured.
//
// Required environment variables for EVM publishing:
//
//	ORDER_VERIFIER_RPC_URL     — JSON-RPC endpoint, e.g. "http://127.0.0.1:8545"
//	ORDER_VERIFIER_CONTRACT    — deployed OrderVerifier contract address (0x...)
//	ORDER_VERIFIER_PRIVATE_KEY — hex-encoded ECDSA private key used to sign txs
//	ORDER_VERIFIER_CHAIN_ID    — EVM chain ID as a decimal integer, e.g. "31337"
//
// Behaviour:
//   - If any variable is missing → returns NoopPublisher (no error). This allows
//     the sequencer to start without EVM access in local/dev mode.
//   - If all variables are present but ORDER_VERIFIER_CHAIN_ID is not a valid
//     integer → returns an error (misconfiguration, fail fast).
//   - If all variables are present and valid → returns a live EVMPublisher.
func NewPublisherFromEnv() (CommitmentPublisher, error) {
	rpcURL := os.Getenv("ORDER_VERIFIER_RPC_URL")
	contract := os.Getenv("ORDER_VERIFIER_CONTRACT")
	pk := os.Getenv("ORDER_VERIFIER_PRIVATE_KEY")
	chainIDRaw := os.Getenv("ORDER_VERIFIER_CHAIN_ID")

	// If any required variable is absent, silently degrade to no-op.
	if rpcURL == "" || contract == "" || pk == "" || chainIDRaw == "" {
		return NoopPublisher{}, nil
	}

	chainIDInt, err := strconv.ParseInt(chainIDRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid ORDER_VERIFIER_CHAIN_ID: %w", err)
	}

	pub, err := NewEVMPublisher(rpcURL, contract, pk, big.NewInt(chainIDInt))
	if err != nil {
		return nil, err
	}
	return pub, nil
}
