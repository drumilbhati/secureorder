package sequencing

import (
	"context"
	"testing"
	"time"
)

func TestRaftOrderedLogSingleNodeCommit(t *testing.T) {
	log, err := NewRaftOrderedLog(RaftOrderedLogConfig{
		NodeID:      "node-1",
		BindAddress: "127.0.0.1:0",
		Bootstrap:   true,
	})
	if err != nil {
		t.Fatalf("NewRaftOrderedLog: %v", err)
	}
	defer log.Close()

	waitForLeader(t, log, 5*time.Second)

	tx, err := log.SubmitWithReceipt(context.Background(), []byte("raft-ciphertext"))
	if err != nil {
		t.Fatalf("SubmitWithReceipt: %v", err)
	}
	if tx.ID == 0 {
		t.Fatal("expected committed log index to be assigned")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batch, err := log.DrainWait(ctx, 1)
	if err != nil {
		t.Fatalf("DrainWait: %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("expected 1 committed tx, got %d", len(batch))
	}
	if batch[0].ID != tx.ID {
		t.Fatalf("expected committed ID %d, got %d", tx.ID, batch[0].ID)
	}
	if string(batch[0].Ciphertext) != "raft-ciphertext" {
		t.Fatalf("unexpected ciphertext %q", string(batch[0].Ciphertext))
	}
}

func waitForLeader(t *testing.T, log *RaftOrderedLog, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if log.raft.State() == 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for raft leader election")
}
