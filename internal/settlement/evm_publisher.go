//go:build evm

// This file is compiled only when the "evm" build tag is present:
//   go build -tags evm ./cmd/sequencer
// Without this tag the stub in evm_publisher_stub.go is used instead,
// allowing the sequencer to compile without the go-ethereum dependency.

package settlement

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// orderVerifierABI is the JSON ABI of the OrderVerifier smart contract.
// We only need the commitOrder function — the rest of the contract is omitted
// to keep the ABI minimal and avoid importing a full Solidity build artefact.
//
// commitOrder(bytes32 commitment) records the batch commitment on-chain.
// The bytes32 type maps to [32]byte in Go.
const orderVerifierABI = `[
  {
    "inputs": [
      {
        "internalType": "bytes32",
        "name": "commitment",
        "type": "bytes32"
      }
    ],
    "name": "commitOrder",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  }
]`

// EVMPublisher sends batch commitments to the OrderVerifier smart contract on
// an Ethereum-compatible chain.
//
// A mutex guards the transactor auth object because go-ethereum's bind package
// reuses nonce state internally — concurrent calls would produce duplicate
// nonces, causing one transaction to be rejected.
type EVMPublisher struct {
	mu       sync.Mutex           // serialises calls to contract.Transact
	client   *ethclient.Client    // JSON-RPC connection to the EVM node
	contract *bind.BoundContract  // pre-bound to the OrderVerifier address
	authBase *bind.TransactOpts   // signer template — copied per call, never mutated
}

// NewEVMPublisher dials rpcURL, parses the ABI, and constructs the transactor
// from the provided private key and chain ID.
//
// The EVMPublisher holds a persistent ethclient.Client connection.
// Call Close (not yet implemented) or let the process exit to release it.
func NewEVMPublisher(rpcURL, contractAddress, privateKeyHex string, chainID *big.Int) (*EVMPublisher, error) {
	if rpcURL == "" || contractAddress == "" || privateKeyHex == "" {
		return nil, errors.New("rpcURL, contractAddress and privateKeyHex are required")
	}
	if chainID == nil {
		return nil, errors.New("chainID is required")
	}

	// Dial the JSON-RPC endpoint. This does not yet send any requests.
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}

	// Parse the minimal ABI. If the ABI is ever extended (e.g. additional
	// events or view functions), only this constant needs updating.
	parsedABI, err := abi.JSON(strings.NewReader(orderVerifierABI))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}

	// Bind the contract to its deployed address. The same client is used for
	// calls, transacts, and event filtering (third, fourth, fifth parameters).
	addr := common.HexToAddress(contractAddress)
	bound := bind.NewBoundContract(addr, parsedABI, client, client, client)

	pk, err := parsePrivateKey(privateKeyHex)
	if err != nil {
		return nil, err
	}

	// authBase is a reusable template for transaction signing. We copy it in
	// PublishCommitment and set the context on the copy to avoid data races.
	authBase, err := bind.NewKeyedTransactorWithChainID(pk, chainID)
	if err != nil {
		return nil, fmt.Errorf("create transactor: %w", err)
	}

	return &EVMPublisher{
		client:   client,
		contract: bound,
		authBase: authBase,
	}, nil
}

// parsePrivateKey accepts a hex-encoded ECDSA private key (with or without
// "0x" prefix) and returns the parsed key.
func parsePrivateKey(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	key := strings.TrimPrefix(privateKeyHex, "0x")
	// Validate that the string is valid hex before passing to go-ethereum,
	// so we get a clear error message rather than a cryptic ECDSA failure.
	_, err := hex.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	pk, err := crypto.HexToECDSA(key)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return pk, nil
}

// parseCommitment32 decodes a 64-character hex string (32 bytes) into a
// [32]byte array suitable for passing to the commitOrder ABI method.
// The contract expects a fixed-size bytes32, not a dynamic bytes slice.
func parseCommitment32(commitmentHex string) ([32]byte, error) {
	var out [32]byte
	raw := strings.TrimPrefix(commitmentHex, "0x")
	if len(raw) != 64 {
		return out, fmt.Errorf("commitment must be 32 bytes hex, got len=%d", len(raw))
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return out, fmt.Errorf("decode commitment hex: %w", err)
	}
	copy(out[:], b)
	return out, nil
}

// PublishCommitment submits a commitOrder transaction to the OrderVerifier
// contract and waits for it to be mined.
//
// The mutex ensures only one transaction is in-flight at a time, which avoids
// nonce collisions. The caller's ctx is used as the transaction context so that
// a timeout or cancellation aborts both the submission and the mining wait.
//
// Returns nil if the transaction was mined with status == 1 (success).
// Returns an error if the transaction was submitted but the contract reverted.
func (p *EVMPublisher) PublishCommitment(ctx context.Context, commitmentHex string) error {
	commitment, err := parseCommitment32(commitmentHex)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Copy authBase and set the context on the copy.
	// Never modify authBase itself — it is shared across calls.
	auth := *p.authBase
	auth.Context = ctx

	// Send the commitOrder transaction to the node. The bound contract handles
	// ABI encoding and nonce management via the ethclient.
	tx, err := p.contract.Transact(&auth, "commitOrder", commitment)
	if err != nil {
		return fmt.Errorf("submit commitOrder tx: %w", err)
	}

	// Block until the transaction is included in a block.
	// The receipt contains the execution status: 1 = success, 0 = reverted.
	receipt, err := bind.WaitMined(ctx, p.client, tx)
	if err != nil {
		return fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("commitOrder reverted, tx=%s", tx.Hash().Hex())
	}

	return nil
}
