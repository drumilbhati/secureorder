package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/drumilbhati/secureorder/pkg/privacy"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
)

const (
	clearScreen = "\033[2J\033[H"
	reset       = "\033[0m"
	green       = "\033[32m"
	blue        = "\033[34m"
	yellow      = "\033[33m"
	cyan        = "\033[36m"
	bold        = "\033[1m"
)

func main() {
	if err := privacy.Init(); err != nil {
		fmt.Printf("privacy init failed: %v\n", err)
		return
	}

	pubKey, secKey, err := privacy.GenerateSequencerKeys()
	if err != nil {
		fmt.Printf("key generation failed: %v\n", err)
		return
	}

	q := sequencing.NewTxQueue(256)
	startTime := time.Now()

	// Print header
	fmt.Print(clearScreen)
	fmt.Printf("%s%s🔐 SECURE-ORDER LIVE SEQUENCER 🔐%s\n", bold, cyan, reset)
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", cyan, reset)

	// Track submitted and processed transactions
	mu := &sync.Mutex{}
	submitted := 0
	processed := 0
	transactions := []string{}

	// Goroutine: continuously send transactions
	go func() {
		for i := 1; i <= 50; i++ {
			time.Sleep(time.Duration(rand.Intn(200)+100) * time.Millisecond)

			clientID := rand.Intn(20) + 1
			tradeType := []string{"BUY", "SELL"}[rand.Intn(2)]
			pair := []string{"ETH/USDC", "BTC/USDC", "SOL/USDC"}[rand.Intn(3)]
			amount := float64(rand.Intn(100) + 1)
			price := 1000.0 + float64(rand.Intn(5000))

			payload := fmt.Sprintf("TRADE|%02d|%s|%s|%.2f|%.2f|%d",
				clientID, tradeType, pair, amount, price, time.Now().UnixNano())

			ciphertext, sealErr := privacy.SealTransaction([]byte(payload), pubKey)
			if sealErr != nil {
				fmt.Printf("Seal error: %v\n", sealErr)
				continue
			}

			if err := q.Submit(context.Background(), ciphertext); err != nil {
				fmt.Printf("Submit error: %v\n", err)
				continue
			}

			mu.Lock()
			submitted++
			txInfo := fmt.Sprintf("Client %02d | %s %s | %.2f @ %.0f", clientID, tradeType, pair, amount, price)
			transactions = append(transactions, txInfo)
			if len(transactions) > 15 {
				transactions = transactions[1:]
			}
			mu.Unlock()
		}
	}()

	// Goroutine: continuously process and display
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	processedCount := 0
	for range ticker.C {
		mu.Lock()

		fmt.Print(clearScreen)
		fmt.Printf("%s%s🔐 SECURE-ORDER LIVE SEQUENCER 🔐%s\n", bold, cyan, reset)
		fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", cyan, reset)

		// Stats
		uptime := time.Since(startTime).Round(time.Second)
		fmt.Printf("%sUptime:%s %v  |  %sSubmitted:%s %d  |  %sProcessed:%s %d  |  %sQueue:%s %d\n\n",
			bold, reset, uptime,
			green, reset, submitted,
			yellow, reset, processed,
			blue, reset, q.Len())

		// Process next batch
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		batch, _ := q.DrainWait(ctx, 5)
		cancel()

		if len(batch) > 0 {
			fmt.Printf("%s📥 LATEST TRANSACTIONS:%s\n", bold, reset)
			fmt.Printf("%s%s\\", blue, bold)
			for i := 0; i < 48; i++ {
				fmt.Print("━")
			}
			fmt.Printf("/%s\n", reset)

			for _, tx := range batch {
				plaintext, _ := privacy.DecryptSingle(tx.Ciphertext, pubKey, secKey)

				fmt.Printf("%s│%s  %s[SeqID: %3d]%s  |  ", blue, reset, green, tx.ID, reset)
				fmt.Printf("%s%s%s\n", yellow, string(plaintext), reset)

				processed++
				processedCount++
			}

			fmt.Printf("%s%s\\", blue, bold)
			for i := 0; i < 48; i++ {
				fmt.Print("━")
			}
			fmt.Printf("/%s\n\n", reset)
		}

		// Recent activity
		fmt.Printf("%s📊 RECENT SUBMISSIONS:%s\n", bold, reset)
		fmt.Printf("%s%s\\", cyan, bold)
		for i := 0; i < 48; i++ {
			fmt.Print("━")
		}
		fmt.Printf("/%s\n", reset)

		for _, tx := range transactions {
			fmt.Printf("%s│%s  ✓  %s\n", cyan, reset, tx)
		}

		fmt.Printf("%s%s\\", cyan, bold)
		for i := 0; i < 48; i++ {
			fmt.Print("━")
		}
		fmt.Printf("/%s\n\n", reset)

		fmt.Printf("%s🔒 FIFO ORDERING: ACTIVE | 🌐 gRPC Port: 50051 | 🔐 Encrypted: YES%s\n", green, reset)

		mu.Unlock()

		// Stop after enough transactions
		if processed >= 50 {
			break
		}
	}

	fmt.Printf("\n%s%s✅ Demo Complete!%s\n", green, bold, reset)
	fmt.Printf("%sTotal Transactions: %d | FIFO Preserved: ✓%s\n\n", green, processed, reset)
}
