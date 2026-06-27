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
	// ID is a monotonically increasing sequence number assigned at admission time.
	// In single-node mode this comes from an in-memory counter; in Raft mode it
	// is the Raft log index, making it globally unique across the entire cluster.
	ID uint64

	// Ciphertext is the raw NaCl sealed-box ciphertext produced by the client.
	// It remains opaque until the batch-decrypt stage in cmd/sequencer/main.go.
	Ciphertext []byte

	// ArrivedAt is the sequencer-assigned timestamp recorded at admission time.
	// Together with ID, it defines the total order for FIFO execution.
	ArrivedAt time.Time
}

// ErrQueueClosed is returned by Submit/DrainWait when Close() has been called.
var ErrQueueClosed = errors.New("tx queue closed")

// submitRequest is the message passed from a caller of SubmitWithReceipt to the
// single admission goroutine. Using a channel-based request/response pattern
// ensures all ID assignment and timestamping happens serially on one goroutine,
// giving us a consistent total order without any mutex on the ID counter.
type submitRequest struct {
	ctx        context.Context
	ciphertext []byte
	resp       chan submitResponse // buffered(1): writer never blocks after sending
}

type submitResponse struct {
	tx  EncryptedTransaction
	err error
}

// TxQueue is a FIFO queue for encrypted transactions.
//
// A single admission goroutine (runAdmissionLoop) serialises all successful
// submissions. This means every accepted transaction gets a total order defined
// by (ArrivedAt, ID) without needing locks on the ID counter or timestamp.
//
// The queue satisfies the OrderedLog interface and can be used as a drop-in
// replacement for RaftOrderedLog in single-node deployments.
type TxQueue struct {
	// ch is the committed-transaction channel read by DrainWait callers.
	// Buffered to capacity so the admission loop rarely blocks on a full queue.
	ch chan EncryptedTransaction

	// submitCh receives submitRequests from concurrent callers of SubmitWithReceipt.
	// The admission loop is the only reader, ensuring serial ID assignment.
	submitCh chan submitRequest

	// done is closed by Close() to signal the admission loop to exit.
	done chan struct{}

	closeOnce sync.Once
	nextID    uint64 // only accessed by the admission goroutine — no mutex needed
}

// NewTxQueue creates a TxQueue with the given internal buffer capacity.
//
// capacity controls how many committed transactions can sit in the queue before
// DrainWait consumers apply backpressure to new submissions.
// A capacity of 0 creates an unbuffered (fully synchronous) queue.
func NewTxQueue(capacity int) *TxQueue {
	q := &TxQueue{
		ch:       make(chan EncryptedTransaction, capacity),
		submitCh: make(chan submitRequest, capacity),
		done:     make(chan struct{}),
	}
	go q.runAdmissionLoop()
	return q
}

// Submit enqueues an encrypted transaction and blocks until it is committed.
// It is a thin wrapper around SubmitWithReceipt for callers that don't need
// the returned sequence metadata.
func (q *TxQueue) Submit(ctx context.Context, ciphertext []byte) error {
	_, err := q.SubmitWithReceipt(ctx, ciphertext)
	return err
}

// SubmitWithReceipt enqueues a transaction and returns the assigned
// EncryptedTransaction (with ID and ArrivedAt filled in), allowing callers to
// generate proof-of-reception commitments immediately at ingress.
//
// It is safe to call from multiple goroutines concurrently. The ciphertext slice
// is copied internally so the caller may reuse their buffer after returning.
func (q *TxQueue) SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error) {
	// Deep copy so the queue owns the data regardless of what the caller does
	// with their buffer afterwards.
	payload := make([]byte, len(ciphertext))
	copy(payload, ciphertext)

	req := submitRequest{
		ctx:        ctx,
		ciphertext: payload,
		resp:       make(chan submitResponse, 1), // buffered so the admission loop never blocks
	}

	// Send the request to the admission goroutine.
	select {
	case q.submitCh <- req:
	case <-q.done:
		return EncryptedTransaction{}, ErrQueueClosed
	case <-ctx.Done():
		return EncryptedTransaction{}, ctx.Err()
	}

	// Wait for the admission goroutine to stamp the ID and timestamp.
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
// queue and returns them in FIFO order. It never blocks — if the queue is empty
// it returns an empty slice immediately.
//
// Use Drain for polling loops where blocking would be undesirable.
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
			// Nothing more buffered right now — return what we have.
			return batch
		}
	}
	return batch
}

// DrainWait collects exactly batchSize transactions in FIFO order, blocking
// until that many are available or the context is cancelled.
//
// It returns however many transactions were collected before cancellation.
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

// Len returns the approximate number of transactions currently buffered.
// Because the channel is consumed concurrently, this value may be stale.
func (q *TxQueue) Len() int {
	return len(q.ch)
}

// runAdmissionLoop is the single goroutine responsible for assigning IDs and
// timestamps. All submitted transactions flow through here before being written
// to q.ch, guaranteeing a consistent total order.
//
// The loop exits when q.done is closed, at which point it drains and rejects
// any pending submitRequests and closes q.ch to unblock DrainWait callers.
func (q *TxQueue) runAdmissionLoop() {
	for {
		select {
		case <-q.done:
			// Shutdown: reject everything still in the submit queue.
			q.rejectPendingSubmissions()
			close(q.ch)
			return

		case req := <-q.submitCh:
			// Check if the submitter's context already expired while waiting
			// in the submitCh buffer.
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

			// Assign the next monotonically increasing ID and stamp arrival time.
			q.nextID++
			tx := EncryptedTransaction{
				ArrivedAt:  time.Now(),
				ID:         q.nextID,
				Ciphertext: req.ciphertext,
			}

			// Write the transaction to the committed channel and notify the submitter.
			// Three things can interrupt this select:
			//   1. Normal case: q.ch accepts the tx and we send back the receipt.
			//   2. The submitter's context expired — they no longer care about the receipt.
			//   3. Queue is being shut down.
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

// rejectPendingSubmissions drains whatever remains in submitCh and sends each
// submitter an ErrQueueClosed response. Called during shutdown to unblock any
// goroutines blocked in SubmitWithReceipt's second select.
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

// Close signals that no further transactions will be submitted and initiates a
// graceful shutdown of the admission loop. Safe to call multiple times.
func (q *TxQueue) Close() {
	q.closeOnce.Do(func() {
		close(q.done)
	})
}
