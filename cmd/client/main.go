package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/drumilbhati/secureorder/pkg/privacy"
	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "localhost:12345", "sequencer gRPC address")
	pubKeyPath := flag.String("pubkey", "keys/sequencer_public.key", "path to sequencer public key")
	payload := flag.String("payload", "", "raw transaction payload to encrypt and submit")
	side := flag.String("side", "BUY", "trade side used when -payload is empty")
	pair := flag.String("pair", "ETH/USDC", "trading pair used when -payload is empty")
	amount := flag.Float64("amount", 1.5, "trade amount used when -payload is empty")
	price := flag.Float64("price", 3200.0, "trade price used when -payload is empty")
	timeout := flag.Duration("timeout", 5*time.Second, "RPC timeout")
	flag.Parse()

	conn, err := grpc.NewClient(*addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRPCServiceClient(conn)

	if err := privacy.Init(); err != nil {
		log.Fatalf("privacy init failed: %v", err)
	}

	pubKey, err := privacy.LoadKeyFromFile(*pubKeyPath, privacy.PublicKeyBytes)
	if err != nil {
		log.Fatalf("failed to load sequencer public key (run sequencer first): %v", err)
	}

	txPayload := *payload
	if txPayload == "" {
		txPayload = fmt.Sprintf(
			"TRADE|%s|%s|%.6f|%.6f|%d",
			*side,
			*pair,
			*amount,
			*price,
			time.Now().Unix(),
		)
	}

	ciphertext, err := privacy.SealTransaction([]byte(txPayload), pubKey)
	if err != nil {
		log.Fatalf("failed to seal transaction: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := client.SubmitTx(ctx, &pb.SubmitRequest{
		Ciphertext: ciphertext,
	})
	if err != nil {
		log.Fatalf("RPC failed: %v", err)
	}

	fmt.Printf("Submitted to %s: accepted=%v payload=%q\n", *addr, res.Accepted, txPayload)
}
