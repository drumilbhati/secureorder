package sequencing

import (
	"context"
	"sync"
	"time"
)

// EncryptedMempool stores encrypted transactions between the ordering stage
// (Raft log) and the reveal stage (batch decrypt). Transactions sit here in
// their ciphertext form until a full batch is assembled, so their contents
// remain hidden from any observer — including the sequencer process itself —
// until the reveal phase begins.
//
// The mempool is a simple mutex-protected slice. FIFO order is preserved
// because items are always appended and drained from the front.
type EncryptedMempool struct {
	mu    sync.Mutex
	items []EncryptedTransaction
}

// NewEncryptedMempool allocates a mempool with an initial slice capacity.
// capacity is a hint — the slice will grow beyond it if needed.
func NewEncryptedMempool(capacity int) *EncryptedMempool {
	if capacity < 0 {
		capacity = 0
	}
	return &EncryptedMempool{items: make([]EncryptedTransaction, 0, capacity)}
}

// Add appends a transaction to the back of the mempool in O(1) time.
// Safe to call from multiple goroutines.
func (m *EncryptedMempool) Add(tx EncryptedTransaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, tx)
}

// Size returns the current number of transactions in the mempool.
// The value may be stale by the time the caller uses it.
func (m *EncryptedMempool) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// DrainWait blocks until at least n transactions are available, then removes
// and returns exactly n transactions in FIFO order.
//
// It polls every 100 ms rather than using a condition variable. This is
// intentionally simple — the batch size is large enough (O(1000)) that the
// polling overhead is negligible compared to decryption cost.
//
// Returns nil if the context is cancelled before n transactions arrive.
func (m *EncryptedMempool) DrainWait(ctx context.Context, n int) []EncryptedTransaction {
	if n <= 0 {
		return nil
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		m.mu.Lock()
		if len(m.items) >= n {
			// Clamp n in case items grew past the requested count
			// (shouldn't happen given the >= guard, but be defensive).
			if n > len(m.items) {
				n = len(m.items)
			}
			// Allocate a fresh slice for the output — the caller owns it.
			out := make([]EncryptedTransaction, n)
			copy(out, m.items[:n])
			// Shift remaining items to the front of the backing array in-place
			// to avoid re-allocating the slice on every drain.
			m.items = append(m.items[:0], m.items[n:]...)
			m.mu.Unlock()
			return out
		}
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Re-check mempool size on the next tick.
		}
	}
}

// DrainUpTo removes and returns up to n transactions in FIFO order without
// blocking. If fewer than n transactions are available it returns however many
// exist. Returns nil if the mempool is empty.
func (m *EncryptedMempool) DrainUpTo(n int) []EncryptedTransaction {
	if n <= 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.items) == 0 {
		return nil
	}

	if n > len(m.items) {
		n = len(m.items)
	}

	out := make([]EncryptedTransaction, n)
	copy(out, m.items[:n])
	m.items = append(m.items[:0], m.items[n:]...)
	return out
}

// DrainAll removes and returns every transaction currently in the mempool in
// FIFO order. Returns nil if the mempool is empty. After this call the mempool
// is empty and its backing slice is reset to length 0 (capacity is retained).
func (m *EncryptedMempool) DrainAll() []EncryptedTransaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return nil
	}
	out := make([]EncryptedTransaction, len(m.items))
	copy(out, m.items)
	m.items = m.items[:0] // reset length, keep capacity
	return out
}
