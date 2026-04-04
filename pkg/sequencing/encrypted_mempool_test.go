package sequencing

import (
	"testing"
	"time"
)

func TestEncryptedMempool_FIFOOrder(t *testing.T) {
	m := NewEncryptedMempool(8)

	m.Add(EncryptedTransaction{ID: 1, Ciphertext: []byte("a"), ArrivedAt: time.Now()})
	m.Add(EncryptedTransaction{ID: 2, Ciphertext: []byte("b"), ArrivedAt: time.Now()})
	m.Add(EncryptedTransaction{ID: 3, Ciphertext: []byte("c"), ArrivedAt: time.Now()})

	batch := m.DrainUpTo(2)
	if len(batch) != 2 {
		t.Fatalf("expected 2 drained items, got %d", len(batch))
	}
	if batch[0].ID != 1 || batch[1].ID != 2 {
		t.Fatalf("expected FIFO IDs [1,2], got [%d,%d]", batch[0].ID, batch[1].ID)
	}

	rest := m.DrainAll()
	if len(rest) != 1 || rest[0].ID != 3 {
		t.Fatalf("expected remaining ID [3], got %#v", rest)
	}
}

func TestEncryptedMempool_DrainBounds(t *testing.T) {
	m := NewEncryptedMempool(2)
	m.Add(EncryptedTransaction{ID: 1, Ciphertext: []byte("a"), ArrivedAt: time.Now()})

	if got := m.DrainUpTo(0); got != nil {
		t.Fatalf("expected nil for non-positive drain, got %v", got)
	}

	batch := m.DrainUpTo(5)
	if len(batch) != 1 || batch[0].ID != 1 {
		t.Fatalf("expected single item with ID=1, got %#v", batch)
	}

	if size := m.Size(); size != 0 {
		t.Fatalf("expected empty mempool, got size=%d", size)
	}
}
