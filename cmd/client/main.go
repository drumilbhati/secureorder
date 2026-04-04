package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/drumilbhati/secureorder/pkg/privacy"
	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Connect to server with insecure transport (localhost only)
	conn, err := grpc.NewClient("localhost:12345",
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

	pubKey, err := privacy.LoadKeyFromFile("keys/sequencer_public.key", privacy.PublicKeyBytes)
	if err != nil {
		log.Fatalf("failed to load sequencer public key (run sequencer first): %v", err)
	}

	payload := fmt.Sprintf(
		"TRADE|BUY|ETH/USDC|%.6f|%.6f|%d",
		1.500000,
		3200.000000,
		time.Now().Unix(),
	)

	ciphertext, err := privacy.SealTransaction([]byte(payload), pubKey)
	if err != nil {
		log.Fatalf("failed to seal transaction: %v", err)
	}

	// Submit transaction
	res, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
		Ciphertext: ciphertext,
	})
	if err != nil {
		log.Fatalf("RPC failed: %v", err)
	}

	fmt.Printf("Transaction accepted: %v\n", res.Accepted)
}
