package sequencing

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- EncryptedTransaction ---

func TestSubmitAssignsMonotonicallyIncreasingIDs(t *testing.T) {
	q := NewTxQueue(16)

	for i := 0; i < 5; i++ {
		if err := q.Submit(context.Background(), []byte("tx")); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	batch := q.Drain(5)
	if len(batch) != 5 {
		t.Fatalf("expected 5 transactions, got %d", len(batch))
	}
	for i, tx := range batch {
		if tx.ID != uint64(i+1) {
			t.Errorf("tx[%d]: expected ID %d, got %d", i, i+1, tx.ID)
		}
	}
}

func TestFIFOOrderingUnderConcurrentSubmissions(t *testing.T) {
	const numClients = 50
	q := NewTxQueue(numClients)

	var wg sync.WaitGroup
	wg.Add(numClients)
	for i := 0; i < numClients; i++ {
		go func() {
			defer wg.Done()
			_ = q.Submit(context.Background(), []byte("payload"))
		}()
	}
	wg.Wait()

	batch := q.Drain(numClients)
	if len(batch) != numClients {
		t.Fatalf("expected %d transactions, got %d", numClients, len(batch))
	}

	// IDs must be strictly increasing (FIFO).
	for i := 1; i < len(batch); i++ {
		if batch[i].ID <= batch[i-1].ID {
			t.Errorf("FIFO violated: batch[%d].ID=%d <= batch[%d].ID=%d",
				i, batch[i].ID, i-1, batch[i-1].ID)
		}
	}
}

func TestSubmitStampsArrivalTime(t *testing.T) {
	q := NewTxQueue(4)
	before := time.Now()
	_ = q.Submit(context.Background(), []byte("tx"))
	after := time.Now()

	batch := q.Drain(1)
	if len(batch) == 0 {
		t.Fatal("expected one transaction")
	}

	if batch[0].ArrivedAt.Before(before) || batch[0].ArrivedAt.After(after) {
		t.Errorf("ArrivedAt %v not in window [%v, %v]", batch[0].ArrivedAt, before, after)
	}
}

func TestSubmitCopiesCiphertextSlice(t *testing.T) {
	q := NewTxQueue(4)
	original := []byte("secret")
	_ = q.Submit(context.Background(), original)

	// Mutate the original buffer; the queued transaction must not be affected.
	original[0] = 'X'

	batch := q.Drain(1)
	if len(batch) == 0 {
		t.Fatal("expected one transaction")
	}
	if batch[0].Ciphertext[0] == 'X' {
		t.Error("Submit did not copy ciphertext: mutation of caller buffer affected queued transaction")
	}
}

func TestSubmitRespectsContextCancellation(t *testing.T) {
	// Unbuffered queue: Submit will block until drained or context cancelled.
	q := NewTxQueue(0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := q.Submit(ctx, []byte("tx"))
	if err == nil {
		t.Fatal("expected Submit to return a context error when buffer is full")
	}
}

// --- Drain ---

func TestDrainReturnsEmptyWhenQueueIsEmpty(t *testing.T) {
	q := NewTxQueue(8)
	batch := q.Drain(4)
	if len(batch) != 0 {
		t.Errorf("expected empty batch, got %d items", len(batch))
	}
}

func TestDrainRespectsMaxBatch(t *testing.T) {
	q := NewTxQueue(16)
	for i := 0; i < 10; i++ {
		_ = q.Submit(context.Background(), []byte("tx"))
	}

	batch := q.Drain(4)
	if len(batch) != 4 {
		t.Errorf("expected 4 transactions, got %d", len(batch))
	}
	// Remaining 6 should still be in queue.
	if q.Len() != 6 {
		t.Errorf("expected 6 remaining, got %d", q.Len())
	}
}

// --- DrainWait ---

func TestDrainWaitCollectsExactBatch(t *testing.T) {
	q := NewTxQueue(16)

	// Submit 8 transactions concurrently while DrainWait is blocking.
	go func() {
		for i := 0; i < 8; i++ {
			_ = q.Submit(context.Background(), []byte("tx"))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batch, err := q.DrainWait(ctx, 8)
	if err != nil {
		t.Fatalf("DrainWait returned unexpected error: %v", err)
	}
	if len(batch) != 8 {
		t.Errorf("expected 8 transactions, got %d", len(batch))
	}
}

func TestDrainWaitReturnsPartialBatchOnContextCancel(t *testing.T) {
	const total = 5
	q := NewTxQueue(16)
	for i := 0; i < total; i++ {
		_ = q.Submit(context.Background(), []byte("tx"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so DrainWait cannot block for the remaining items.
	cancel()

	batch, err := q.DrainWait(ctx, total+10)
	if err == nil {
		t.Fatal("expected a context error")
	}
	if len(batch) > total {
		t.Errorf("batch len %d exceeds submitted %d", len(batch), total)
	}
}

// --- Len ---

func TestLen(t *testing.T) {
	q := NewTxQueue(8)
	if q.Len() != 0 {
		t.Fatalf("new queue should be empty")
	}
	for i := 0; i < 3; i++ {
		_ = q.Submit(context.Background(), []byte("tx"))
	}
	if q.Len() != 3 {
		t.Errorf("expected Len 3, got %d", q.Len())
	}
}
