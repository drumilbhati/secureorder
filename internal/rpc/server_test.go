package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drumilbhati/secureorder/pkg/sequencing"
	pb "github.com/drumilbhati/secureorder/proto"
)

type stubOrderedLog struct {
	submit func(ctx context.Context, ciphertext []byte) (sequencing.EncryptedTransaction, error)
}

func (s stubOrderedLog) SubmitWithReceipt(ctx context.Context, ciphertext []byte) (sequencing.EncryptedTransaction, error) {
	return s.submit(ctx, ciphertext)
}

func (stubOrderedLog) DrainWait(context.Context, int) ([]sequencing.EncryptedTransaction, error) {
	return nil, nil
}

func (stubOrderedLog) Close() {}

func TestSubmitTx_Success(t *testing.T) {
	queue := sequencing.NewTxQueue(10)
	server := NewServer(queue)

	req := &pb.SubmitRequest{
		Ciphertext: []byte("test-encrypted-data"),
	}

	ack, err := server.SubmitTx(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ack.Accepted {
		t.Errorf("expected accepted=true, got false")
	}

	// Verify the transaction was actually queued
	if queue.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", queue.Len())
	}

	// Verify the ciphertext matches
	txs := queue.Drain(1)
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction in queue, got %d", len(txs))
	}
	if string(txs[0].Ciphertext) != "test-encrypted-data" {
		t.Errorf("expected ciphertext 'test-encrypted-data', got %q", string(txs[0].Ciphertext))
	}

	if server.ProofCount() != 1 {
		t.Errorf("expected 1 reception proof, got %d", server.ProofCount())
	}
}

func TestSubmitTx_ContextCancelled(t *testing.T) {
	// Create an unbuffered queue (capacity=0) so Submit blocks
	queue := sequencing.NewTxQueue(0)
	server := NewServer(queue)

	req := &pb.SubmitRequest{
		Ciphertext: []byte("test-data"),
	}

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ack, err := server.SubmitTx(ctx, req)
	if err == nil {
		t.Error("expected error when context is cancelled, got nil")
	}
	if ack.Accepted {
		t.Error("expected accepted=false on error, got true")
	}
}

func TestSubmitTx_UsesOrderedLogAbstraction(t *testing.T) {
	expected := sequencing.EncryptedTransaction{
		ID:         42,
		Ciphertext: []byte("via-stub"),
		ArrivedAt:  time.Unix(1700000000, 99),
	}

	server := NewServer(stubOrderedLog{
		submit: func(ctx context.Context, ciphertext []byte) (sequencing.EncryptedTransaction, error) {
			if string(ciphertext) != "via-stub" {
				t.Fatalf("unexpected ciphertext %q", string(ciphertext))
			}
			return expected, nil
		},
	})

	ack, err := server.SubmitTx(context.Background(), &pb.SubmitRequest{Ciphertext: []byte("via-stub")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ack.Accepted {
		t.Fatal("expected accepted=true")
	}
	if server.ProofCount() != 1 {
		t.Fatalf("expected 1 reception proof, got %d", server.ProofCount())
	}
}

func TestSubmitTx_PropagatesOrderedLogError(t *testing.T) {
	server := NewServer(stubOrderedLog{
		submit: func(context.Context, []byte) (sequencing.EncryptedTransaction, error) {
			return sequencing.EncryptedTransaction{}, errors.New("proposal failed")
		},
	})

	ack, err := server.SubmitTx(context.Background(), &pb.SubmitRequest{Ciphertext: []byte("x")})
	if err == nil {
		t.Fatal("expected error")
	}
	if ack.Accepted {
		t.Fatal("expected accepted=false")
	}
}

func TestSubmitTx_ContextDeadline(t *testing.T) {
	// Create an unbuffered queue so Submit blocks
	queue := sequencing.NewTxQueue(0)
	server := NewServer(queue)

	req := &pb.SubmitRequest{
		Ciphertext: []byte("test-data"),
	}

	// Create a context with a short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	ack, err := server.SubmitTx(ctx, req)
	if err == nil {
		t.Error("expected error when context deadline exceeded, got nil")
	}
	if ack.Accepted {
		t.Error("expected accepted=false on error, got true")
	}
}

func TestSubmitTx_EmptyCiphertext(t *testing.T) {
	queue := sequencing.NewTxQueue(10)
	server := NewServer(queue)

	req := &pb.SubmitRequest{
		Ciphertext: []byte{},
	}

	ack, err := server.SubmitTx(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ack.Accepted {
		t.Error("expected accepted=true for empty ciphertext")
	}
}

func TestSubmitTx_LargeCiphertext(t *testing.T) {
	queue := sequencing.NewTxQueue(10)
	server := NewServer(queue)

	// Test with 1MB of data
	largeData := make([]byte, 1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	req := &pb.SubmitRequest{
		Ciphertext: largeData,
	}

	ack, err := server.SubmitTx(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ack.Accepted {
		t.Error("expected accepted=true for large ciphertext")
	}
}

func TestSubmitTx_ReceptionProofMonotonic(t *testing.T) {
	queue := sequencing.NewTxQueue(10)
	server := NewServer(queue)

	for i := 0; i < 3; i++ {
		_, err := server.SubmitTx(context.Background(), &pb.SubmitRequest{Ciphertext: []byte("ct")})
		if err != nil {
			t.Fatalf("submit %d failed: %v", i, err)
		}
	}

	if server.ProofCount() != 3 {
		t.Fatalf("expected 3 proofs, got %d", server.ProofCount())
	}
}
