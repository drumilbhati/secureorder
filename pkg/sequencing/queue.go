package sequencing

import (
	"context"
	"errors"
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

var ErrQueueClosed = errors.New("tx queue closed")

type submitRequest struct {
	ctx        context.Context
	ciphertext []byte
	resp       chan submitResponse
}

type submitResponse struct {
	tx  EncryptedTransaction
	err error
}

// TxQueue is a FIFO queue for encrypted transactions.
// A dedicated admission goroutine serializes successful submissions so every
// accepted transaction gets a total order defined by (ArrivedAt, ID).
type TxQueue struct {
	ch       chan EncryptedTransaction
	submitCh chan submitRequest
	done     chan struct{}

	closeOnce sync.Once
	nextID    uint64
}

// NewTxQueue creates a TxQueue with the given internal buffer capacity.
// capacity controls how many transactions can be buffered before Submit blocks.
// A capacity of 0 creates an unbuffered (synchronous) queue.
func NewTxQueue(capacity int) *TxQueue {
	q := &TxQueue{
		ch:       make(chan EncryptedTransaction, capacity),
		submitCh: make(chan submitRequest, capacity),
		done:     make(chan struct{}),
	}
	go q.runAdmissionLoop()
	return q
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
	// Copy the slice so the queue owns the data regardless of what the caller does with their buffer afterwards.
	payload := make([]byte, len(ciphertext))
	copy(payload, ciphertext)

	req := submitRequest{
		ctx:        ctx,
		ciphertext: payload,
		resp:       make(chan submitResponse, 1),
	}

	select {
	case q.submitCh <- req:
	case <-q.done:
		return EncryptedTransaction{}, ErrQueueClosed
	case <-ctx.Done():
		return EncryptedTransaction{}, ctx.Err()
	}

	select {
	case resp := <-req.resp:
		return resp.tx, resp.err
	case <-q.done:
		return EncryptedTransaction{}, ErrQueueClosed
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
		case tx, ok := <-q.ch:
			if !ok {
				return batch
			}
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
		case tx, ok := <-q.ch:
			if !ok {
				return batch, ErrQueueClosed
			}
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

func (q *TxQueue) runAdmissionLoop() {
	for {
		select {
		case <-q.done:
			q.rejectPendingSubmissions()
			close(q.ch)
			return
		case req := <-q.submitCh:
			select {
			case <-req.ctx.Done():
				req.resp <- submitResponse{err: req.ctx.Err()}
				continue
			case <-q.done:
				req.resp <- submitResponse{err: ErrQueueClosed}
				q.rejectPendingSubmissions()
				close(q.ch)
				return
			default:
			}

			q.nextID++
			tx := EncryptedTransaction{
				ArrivedAt:  time.Now(),
				ID:         q.nextID,
				Ciphertext: req.ciphertext,
			}

			select {
			case q.ch <- tx:
				req.resp <- submitResponse{tx: tx}
			case <-req.ctx.Done():
				req.resp <- submitResponse{err: req.ctx.Err()}
			case <-q.done:
				req.resp <- submitResponse{err: ErrQueueClosed}
				q.rejectPendingSubmissions()
				close(q.ch)
				return
			}
		}
	}
}

func (q *TxQueue) rejectPendingSubmissions() {
	for {
		select {
		case req := <-q.submitCh:
			req.resp <- submitResponse{err: ErrQueueClosed}
		default:
			return
		}
	}
}

// Close signals that no further transactions will be submitted and eventually
// closes the internal transaction channel after pending submissions are rejected.
func (q *TxQueue) Close() {
	q.closeOnce.Do(func() {
		close(q.done)
	})
}
