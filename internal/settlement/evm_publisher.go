//go:build evm

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

type EVMPublisher struct {
	mu       sync.Mutex
	client   *ethclient.Client
	contract *bind.BoundContract
	authBase *bind.TransactOpts
}

func NewEVMPublisher(rpcURL, contractAddress, privateKeyHex string, chainID *big.Int) (*EVMPublisher, error) {
	if rpcURL == "" || contractAddress == "" || privateKeyHex == "" {
		return nil, errors.New("rpcURL, contractAddress and privateKeyHex are required")
	}
	if chainID == nil {
		return nil, errors.New("chainID is required")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}

	parsedABI, err := abi.JSON(strings.NewReader(orderVerifierABI))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}

	addr := common.HexToAddress(contractAddress)
	bound := bind.NewBoundContract(addr, parsedABI, client, client, client)

	pk, err := parsePrivateKey(privateKeyHex)
	if err != nil {
		return nil, err
	}

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

func parsePrivateKey(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	key := strings.TrimPrefix(privateKeyHex, "0x")
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

func (p *EVMPublisher) PublishCommitment(ctx context.Context, commitmentHex string) error {
	commitment, err := parseCommitment32(commitmentHex)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	auth := *p.authBase
	auth.Context = ctx

	tx, err := p.contract.Transact(&auth, "commitOrder", commitment)
	if err != nil {
		return fmt.Errorf("submit commitOrder tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, p.client, tx)
	if err != nil {
		return fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("commitOrder reverted, tx=%s", tx.Hash().Hex())
	}

	return nil
}
