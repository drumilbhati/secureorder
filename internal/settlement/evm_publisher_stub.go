//go:build !evm

package settlement

import (
	"context"
	"errors"
	"math/big"
)

func NewEVMPublisher(_ string, _ string, _ string, _ *big.Int) (*EVMPublisher, error) {
	return nil, errors.New("evm publisher disabled: build with -tags evm")
}

type EVMPublisher struct{}

func (EVMPublisher) PublishCommitment(_ context.Context, _ string) error {
	return errors.New("evm publisher disabled: build with -tags evm")
}
