package settlement

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
)

// NewPublisherFromEnv enables EVM commitment publishing when all env vars exist.
// Required vars:
//
//	ORDER_VERIFIER_RPC_URL
//	ORDER_VERIFIER_CONTRACT
//	ORDER_VERIFIER_PRIVATE_KEY
//	ORDER_VERIFIER_CHAIN_ID
func NewPublisherFromEnv() (CommitmentPublisher, error) {
	rpcURL := os.Getenv("ORDER_VERIFIER_RPC_URL")
	contract := os.Getenv("ORDER_VERIFIER_CONTRACT")
	pk := os.Getenv("ORDER_VERIFIER_PRIVATE_KEY")
	chainIDRaw := os.Getenv("ORDER_VERIFIER_CHAIN_ID")

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
