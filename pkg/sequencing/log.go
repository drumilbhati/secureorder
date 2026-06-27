package sequencing

import "context"

// OrderedLog is the sequencing boundary between transaction ingress (gRPC) and
// the batch-decrypt stage.
//
// Any implementation must guarantee:
//   - SubmitWithReceipt assigns a globally unique, monotonically increasing ID
//     to each accepted transaction.
//   - DrainWait returns transactions in the order they were committed, i.e.
//     sorted by ID ascending.
//   - Concurrent calls to SubmitWithReceipt are safe.
//
// Two concrete implementations exist:
//   - TxQueue   — single-node, in-memory. IDs come from an atomic counter.
//   - RaftOrderedLog — multi-node, consensus-backed. IDs are Raft log indices.
//
// The interface is intentionally narrow: callers only need to submit and drain.
// Raft-specific concerns (leadership, cluster membership) are handled by type
// assertions in the gRPC layer where they are needed.
type OrderedLog interface {
	// SubmitWithReceipt commits ciphertext to the log and returns the assigned
	// EncryptedTransaction. Blocks until committed (or ctx is cancelled).
	SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error)

	// DrainWait blocks until batchSize transactions are available, then returns
	// them in FIFO order. Returns early with whatever is available on ctx cancel.
	DrainWait(ctx context.Context, batchSize int) ([]EncryptedTransaction, error)

	// Close shuts down the backend. Safe to call multiple times.
	Close()
}

// Compile-time assertion: TxQueue must satisfy OrderedLog.
var _ OrderedLog = (*TxQueue)(nil)
