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
		numClients = 10
		batchSize  = numClients
		queueCap   = 64
	)

	q := sequencing.NewTxQueue(queueCap)

	if err := privacy.Init(); err != nil {
		fmt.Printf("privacy init failed: %v\n", err)
		return
	}

	pubKey, secKey, err := privacy.GenerateSequencerKeys()
	if err != nil {
		fmt.Printf("key generation failed: %v\n", err)
		return
	}

	// Simulate numClients submitting encrypted transactions concurrently.
	var wg sync.WaitGroup
	wg.Add(numClients)
	for i := 1; i <= numClients; i++ {
		go func(clientID int) {
			defer wg.Done()
			payload := fmt.Sprintf("TRADE|%02d|BUY|ETH/USDC|%.6f|%.6f|%d", clientID, float64(clientID), 3000.0+float64(clientID), time.Now().Unix())
			ciphertext, sealErr := privacy.SealTransaction([]byte(payload), pubKey)
			if sealErr != nil {
				fmt.Printf("client %d seal error: %v\n", clientID, sealErr)
				return
			}
			if err := q.Submit(context.Background(), ciphertext); err != nil {
				fmt.Printf("client %d submit error: %v\n", clientID, err)
			}
		}(i)
	}

	// Wait for all clients to finish submitting, then drain the queue.
	wg.Wait()

	fmt.Printf("\nQueue length after submissions: %d\n\n", q.Len())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batch, err := q.DrainWait(ctx, batchSize)
	if err != nil {
		fmt.Printf("DrainWait error: %v\n", err)
	}

	fmt.Printf("%-6s  %-48s  %-30s  %s\n", "SeqID", "Ciphertext(prefix)", "Plaintext", "ArrivedAt")
	fmt.Println("------  ------------------------------------------------  ------------------------------  ------------------------")
	for _, tx := range batch {
		plaintext, decErr := privacy.DecryptSingle(tx.Ciphertext, pubKey, secKey)
		if decErr != nil {
			plaintext = []byte("<decrypt-error>")
		}

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

	// Verify IDs are strictly increasing (FIFO invariant).
	fifo := true
	for i := 1; i < len(batch); i++ {
		if batch[i].ID <= batch[i-1].ID {
			fifo = false
			break
		}
	}
	fmt.Printf("\nFIFO order preserved: %v\n", fifo)
}
