package sequencing

import (
	"context"
	"sync"
	"time"
)

// EncryptedTransaction holds an encrypted payload submitted by a client,
// along with metadata assigned by the sequencer upon arrival.
type EncryptedTransaction struct {
	// ID is a monotonically increasing tie-breaker assigned at admission time.
	ID uint64

	// Ciphertext is the raw NaCl sealed-box ciphertext produced by the client.
	Ciphertext []byte

	// ArrivedAt is the sequencer-assigned timestamp recorded at admission time.
	ArrivedAt time.Time
}

// TxQueue is a FIFO queue for encrypted transactions.
// Concurrent submissions are serialized through a mutex so every accepted
// transaction gets a total order defined by (ArrivedAt, ID).
type TxQueue struct {
	ch     chan EncryptedTransaction
	nextID uint64
	mu     sync.Mutex
}

// NewTxQueue creates a TxQueue with the given internal buffer capacity.
// capacity controls how many transactions can be buffered before Submit blocks.
// A capacity of 0 creates an unbuffered (synchronous) queue.
func NewTxQueue(capacity int) *TxQueue {
	return &TxQueue{
		ch: make(chan EncryptedTransaction, capacity),
	}
}

// Submit enqueues an encrypted transaction. It stamps the sequencer arrival
// time first and assigns a monotonically increasing ID to break timestamp ties,
// so successful submissions produce a consistent total order.
//
// Submit blocks when the internal buffer is full. Pass a context with a
// deadline or cancellation to bound the wait:
//
//	err := q.Submit(ctx, ciphertext)
func (q *TxQueue) Submit(ctx context.Context, ciphertext []byte) error {
	_, err := q.SubmitWithReceipt(ctx, ciphertext)
	return err
}

// SubmitWithReceipt enqueues a transaction and returns the assigned sequence
// metadata, allowing callers to create proof-of-reception commitments at ingress.
func (q *TxQueue) SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error) {
	// Copy the slice so the queue owns the data regardless of what the
	// caller does with their buffer afterwards.
	payload := make([]byte, len(ciphertext))
	copy(payload, ciphertext)

	q.mu.Lock()
	defer q.mu.Unlock()

	q.nextID++
	tx := EncryptedTransaction{
		ArrivedAt:  time.Now(),
		ID:         q.nextID,
		Ciphertext: payload,
	}

	select {
	case q.ch <- tx:
		return tx, nil
	case <-ctx.Done():
		return EncryptedTransaction{}, ctx.Err()
	}
}

// Drain collects up to maxBatch transactions that are already buffered in the
// queue and returns them in FIFO order. It never blocks waiting for new
// submissions; if the queue is empty it returns an empty slice immediately.
//
// Use Drain when you want to process whatever is available right now without
// waiting for a full batch.
func (q *TxQueue) Drain(maxBatch int) []EncryptedTransaction {
	batch := make([]EncryptedTransaction, 0, maxBatch)
	for len(batch) < maxBatch {
		select {
		case tx := <-q.ch:
			batch = append(batch, tx)
		default:
			return batch
		}
	}
	return batch
}

// DrainWait collects exactly batchSize transactions in FIFO order, blocking
// until that many are available or ctx is cancelled. It returns however many
// transactions were collected before the context was cancelled.
//
// This is the preferred method for a batch-processing loop that should wait
// for a full batch before proceeding.
func (q *TxQueue) DrainWait(ctx context.Context, batchSize int) ([]EncryptedTransaction, error) {
	batch := make([]EncryptedTransaction, 0, batchSize)
	for len(batch) < batchSize {
		select {
		case tx := <-q.ch:
			batch = append(batch, tx)
		case <-ctx.Done():
			return batch, ctx.Err()
		}
	}
	return batch, nil
}

// Len returns the number of transactions currently buffered in the queue.
// Because the channel is consumed concurrently, this value is approximate.
func (q *TxQueue) Len() int {
	return len(q.ch)
}

// Close signals that no further transactions will be submitted. Any goroutine
// blocked in DrainWait will receive the remaining buffered transactions and
// then get a closed-channel read, so callers should check for the zero value.
// Calling Close more than once panics (same as closing a Go channel twice).
func (q *TxQueue) Close() {
	close(q.ch)
}
