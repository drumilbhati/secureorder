package sequencing

import "context"

// OrderedLog is the sequencing boundary between transaction ingress and the
// component that commits encrypted transactions into a canonical order.
//
// The current implementation is a single-node in-memory queue. A future
// implementation can back this interface with a replicated consensus log
// without changing the RPC ingress layer.
type OrderedLog interface {
	SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error)
	DrainWait(ctx context.Context, batchSize int) ([]EncryptedTransaction, error)
	Close()
}

var _ OrderedLog = (*TxQueue)(nil)
