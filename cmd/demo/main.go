// Package main is a self-contained in-process demo of the Secure-Order pipeline.
//
// Unlike the production sequencer (cmd/sequencer) which runs as a gRPC service,
// this demo runs the entire pipeline in a single process:
//   1. Multiple goroutines concurrently submit encrypted transactions to a local TxQueue.
//   2. After all submissions complete, the queue is drained in one batch.
//   3. Each transaction is decrypted and its metadata is printed.
//   4. FIFO ordering is verified by checking that sequence IDs are strictly increasing.
//
// The demo is useful for verifying that the C++ privacy layer builds correctly
// and that the end-to-end encrypt → queue → decrypt path works as expected.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drumilbhati/secureorder/pkg/privacy"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
)

func main() {
	const (
		numClients = 10  // number of concurrent client goroutines
		batchSize  = numClients // drain exactly numClients transactions
		queueCap   = 64  // internal TxQueue channel buffer capacity
	)

	// ── Create local ordering queue ────────────────────────────────────────
	// TxQueue is the single-node equivalent of RaftOrderedLog.
	// It assigns monotonically increasing IDs via a serial admission loop.
	q := sequencing.NewTxQueue(queueCap)

	// ── Initialise libsodium ───────────────────────────────────────────────
	if err := privacy.Init(); err != nil {
		fmt.Printf("privacy init failed: %v\n", err)
		return
	}

	// ── Generate a fresh keypair for this demo session ─────────────────────
	// In production, keys are persisted to disk by loadOrCreateSequencerKeys().
	// For the demo, ephemeral keys are fine.
	pubKey, secKey, err := privacy.GenerateSequencerKeys()
	if err != nil {
		fmt.Printf("key generation failed: %v\n", err)
		return
	}

	// ── Concurrent client simulation ───────────────────────────────────────
	// Each goroutine simulates a trading bot that builds a plaintext payload,
	// encrypts it, and submits it to the queue. Goroutines race to submit;
	// the queue's admission loop assigns IDs in the order it receives them.
	// The final FIFO verification at the bottom checks that IDs are ordered.
	var wg sync.WaitGroup
	wg.Add(numClients)
	for i := 1; i <= numClients; i++ {
		go func(clientID int) {
			defer wg.Done()

			// Pipe-delimited trade payload: includes clientID so each transaction
			// is unique and identifiable in the output table.
			payload := fmt.Sprintf(
				"TRADE|%02d|BUY|ETH/USDC|%.6f|%.6f|%d",
				clientID,
				float64(clientID),          // amount varies per client
				3000.0+float64(clientID),   // price varies per client
				time.Now().Unix(),
			)

			// Encrypt with the sequencer's public key. The client is anonymous —
			// the sequencer cannot tell which client sent this until after decryption.
			ciphertext, sealErr := privacy.SealTransaction([]byte(payload), pubKey)
			if sealErr != nil {
				fmt.Printf("client %d seal error: %v\n", clientID, sealErr)
				return
			}

			// Submit to the queue. context.Background() means no timeout —
			// acceptable in this demo because the queue is local and fast.
			if err := q.Submit(context.Background(), ciphertext); err != nil {
				fmt.Printf("client %d submit error: %v\n", clientID, err)
			}
		}(i)
	}

	// Wait for all client goroutines to finish before draining.
	wg.Wait()

	fmt.Printf("\nQueue length after submissions: %d\n\n", q.Len())

	// ── Batch drain ────────────────────────────────────────────────────────
	// DrainWait blocks until exactly batchSize transactions are available.
	// We use a 2-second timeout as a safety net in case a goroutine silently
	// failed to submit (which would leave the queue short of batchSize).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batch, err := q.DrainWait(ctx, batchSize)
	if err != nil {
		fmt.Printf("DrainWait error: %v\n", err)
	}

	// ── Decrypt and print results ──────────────────────────────────────────
	// Print a table: SeqID | ciphertext prefix (for uniqueness) | plaintext | arrival time.
	// The ciphertext prefix is shown as hex — the full ciphertext is 48+ bytes.
	fmt.Printf("%-6s  %-48s  %-30s  %s\n", "SeqID", "Ciphertext(prefix)", "Plaintext", "ArrivedAt")
	fmt.Println("------  ------------------------------------------------  ------------------------------  ------------------------")
	for _, tx := range batch {
		plaintext, decErr := privacy.DecryptSingle(tx.Ciphertext, pubKey, secKey)
		if decErr != nil {
			plaintext = []byte("<decrypt-error>")
		}

		// Show only the first 24 bytes of the ciphertext to keep the table readable.
		prefix := tx.Ciphertext
		if len(prefix) > 24 {
			prefix = prefix[:24]
		}

		fmt.Printf("%-6d  %-48x  %-30s  %s\n",
			tx.ID,
			prefix,
			string(plaintext),
			tx.ArrivedAt.Format("15:04:05.000000000"),
		)
	}

	// ── FIFO verification ──────────────────────────────────────────────────
	// Verify the FIFO invariant: every transaction's ID must be strictly
	// greater than the previous one. If this fails, the admission loop has
	// a bug that allowed out-of-order ID assignment.
	fifo := true
	for i := 1; i < len(batch); i++ {
		if batch[i].ID <= batch[i-1].ID {
			fifo = false
			break
		}
	}
	fmt.Printf("\nFIFO order preserved: %v\n", fifo)
}
