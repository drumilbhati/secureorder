package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/drumilbhati/secureorder/internal/rpc"
	"github.com/drumilbhati/secureorder/pkg/privacy"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
	"google.golang.org/grpc"
)

func loadOrCreateSequencerKeys() ([]byte, []byte, error) {
	if err := os.MkdirAll("keys", 0o700); err != nil {
		return nil, nil, fmt.Errorf("failed to create key directory: %w", err)
	}

	const pubPath = "keys/sequencer_public.key"
	const secPath = "keys/sequencer_private.key"

	pub, pubErr := privacy.LoadKeyFromFile(pubPath, privacy.PublicKeyBytes)
	sec, secErr := privacy.LoadKeyFromFile(secPath, privacy.SecretKeyBytes)
	if pubErr == nil && secErr == nil {
		return pub, sec, nil
	}

	pub, sec, err := privacy.GenerateSequencerKeys()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate sequencer keypair: %w", err)
	}

	if err := privacy.SaveKeyToFile(pubPath, pub); err != nil {
		return nil, nil, err
	}
	if err := privacy.SaveKeyToFile(secPath, sec); err != nil {
		return nil, nil, err
	}

	return pub, sec, nil
}

func main() {
	if err := privacy.Init(); err != nil {
		log.Fatalf("privacy init failed: %v", err)
	}

	pubKey, secKey, err := loadOrCreateSequencerKeys()
	if err != nil {
		log.Fatalf("sequencer key setup failed: %v", err)
	}

	fmt.Println("Sequencer keys ready in keys/")

	orderedLog := sequencing.NewTxQueue(100)
	mempool := sequencing.NewEncryptedMempool(1024)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// Ingress collector: keep transactions encrypted in mempool until reveal.
		for {
			txs, err := orderedLog.DrainWait(ctx, 1)
			if err != nil {
				return
			}
			for _, tx := range txs {
				mempool.Add(tx)
			}
		}
	}()

	go func() {
		revealTicker := time.NewTicker(300 * time.Millisecond)
		defer revealTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-revealTicker.C:
			}

			batch := mempool.DrainUpTo(10)
			if len(batch) == 0 {
				continue
			}

			ciphertexts := make([][]byte, len(batch))
			for i := range batch {
				ciphertexts[i] = batch[i].Ciphertext
			}

			plaintexts, batchErr := privacy.DecryptBatch(ciphertexts, pubKey, secKey)
			if batchErr == nil {
				for i, tx := range batch {
					fmt.Printf("Processed tx ID=%d, plaintext=%s\n", tx.ID, string(plaintexts[i]))
				}
				continue
			}

			log.Printf("batch decrypt failed, falling back to per-item decrypt: %v", batchErr)
			for _, tx := range batch {
				plaintext, decErr := privacy.DecryptSingle(tx.Ciphertext, pubKey, secKey)
				if decErr != nil {
					log.Printf("failed to decrypt tx ID=%d: %v", tx.ID, decErr)
					continue
				}
				fmt.Printf("Processed tx ID=%d, plaintext=%s\n", tx.ID, string(plaintext))
			}
		}
	}()

	grpcServer := grpc.NewServer()
	rpcServer := rpc.NewServer(orderedLog)
	rpc.Register(grpcServer, rpcServer)

	lis, err := net.Listen("tcp", ":12345")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	go func() {
		fmt.Printf("gRPC server listening on %s\n", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("serve error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	fmt.Println("Shutting down...")
	cancel()
	grpcServer.GracefulStop()
	orderedLog.Close()
	fmt.Println("Server stopped")
}
