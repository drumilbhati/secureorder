package sequencing

import "sync"

// EncryptedMempool stores encrypted transactions until a reveal/decrypt stage drains them.
type EncryptedMempool struct {
	mu    sync.Mutex
	items []EncryptedTransaction
}

func NewEncryptedMempool(capacity int) *EncryptedMempool {
	if capacity < 0 {
		capacity = 0
	}
	return &EncryptedMempool{items: make([]EncryptedTransaction, 0, capacity)}
}

func (m *EncryptedMempool) Add(tx EncryptedTransaction) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, tx)
}

func (m *EncryptedMempool) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// DrainWait blocks until at least n transactions are available or the context is cancelled.
func (m *EncryptedMempool) DrainWait(ctx context.Context, n int) []EncryptedTransaction {
	if n <= 0 {
		return nil
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		m.mu.Lock()
		if len(m.items) >= n {
			if n > len(m.items) {
				n = len(m.items)
			}
			out := make([]EncryptedTransaction, n)
			copy(out, m.items[:n])
			m.items = append(m.items[:0], m.items[n:]...)
			m.mu.Unlock()
			return out
		}
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// DrainUpTo drains up to n transactions in strict FIFO order.
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

func (m *EncryptedMempool) DrainAll() []EncryptedTransaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.items) == 0 {
		return nil
	}
	out := make([]EncryptedTransaction, len(m.items))
	copy(out, m.items)
	m.items = m.items[:0]
	return out
}
