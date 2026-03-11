package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drumilbhati/secureorder/pkg/sequencing"
)

func main() {
	const (
		numClients = 10
		batchSize  = numClients
		queueCap   = 64
	)

	q := sequencing.NewTxQueue(queueCap)

	// Simulate numClients submitting encrypted transactions concurrently.
	var wg sync.WaitGroup
	wg.Add(numClients)
	for i := 1; i <= numClients; i++ {
		go func(clientID int) {
			defer wg.Done()
			payload := fmt.Sprintf("encrypted-tx-from-client-%02d", clientID)
			if err := q.Submit(context.Background(), []byte(payload)); err != nil {
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

	fmt.Printf("%-6s  %-30s  %s\n", "SeqID", "Ciphertext", "ArrivedAt")
	fmt.Println("------  ------------------------------  ------------------------")
	for _, tx := range batch {
		fmt.Printf("%-6d  %-30s  %s\n",
			tx.ID,
			string(tx.Ciphertext),
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
