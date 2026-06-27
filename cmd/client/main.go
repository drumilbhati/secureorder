// Package main is the entry point for the Secure-Order CLI client.
//
// The client encrypts a transaction (or constructs a default trade payload),
// seals it with the sequencer's public key, and submits it over gRPC.
// It is the user-facing tool for interacting with the sequencer in development
// and testing environments.
//
// Usage:
//
//	./client -addr=localhost:12345 -pubkey=keys/sequencer_public.key \
//	         -side=BUY -pair=ETH/USDC -amount=1.5 -price=3200
//	./client -addr=localhost:12345 -payload="TRADE|raw|custom|payload"
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
	// ── CLI flags ──────────────────────────────────────────────────────────
	addr := flag.String("addr", "localhost:12345", "sequencer gRPC address")
	pubKeyPath := flag.String("pubkey", "keys/sequencer_public.key", "path to sequencer public key")
	payload := flag.String("payload", "", "raw transaction payload to encrypt and submit")
	// Trade construction flags — used when -payload is not provided.
	side := flag.String("side", "BUY", "trade side used when -payload is empty")
	pair := flag.String("pair", "ETH/USDC", "trading pair used when -payload is empty")
	amount := flag.Float64("amount", 1.5, "trade amount used when -payload is empty")
	price := flag.Float64("price", 3200.0, "trade price used when -payload is empty")
	timeout := flag.Duration("timeout", 5*time.Second, "RPC timeout")
	flag.Parse()

	// ── Connect to sequencer ───────────────────────────────────────────────
	// grpc.NewClient is the modern (non-deprecated) way to create a channel.
	// insecure.NewCredentials() is appropriate for local/dev — use TLS in prod.
	conn, err := grpc.NewClient(*addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRPCServiceClient(conn)

	// ── Initialise libsodium ───────────────────────────────────────────────
	// Must be called before SealTransaction.
	if err := privacy.Init(); err != nil {
		log.Fatalf("privacy init failed: %v", err)
	}

	// ── Load sequencer public key ──────────────────────────────────────────
	// The client does NOT need the secret key — it only seals (encrypts).
	// The key file is produced by the sequencer on first startup under keys/.
	pubKey, err := privacy.LoadKeyFromFile(*pubKeyPath, privacy.PublicKeyBytes)
	if err != nil {
		log.Fatalf("failed to load sequencer public key (run sequencer first): %v", err)
	}

	// ── Build transaction payload ──────────────────────────────────────────
	// If the user didn't provide a raw payload, construct a pipe-delimited
	// trade string. The format is:
	//   TRADE|<side>|<pair>|<amount>|<price>|<unix_timestamp>
	// The Unix timestamp prevents two identical trade parameters from
	// producing the same plaintext (and thus the same ciphertext).
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

	// ── Encrypt with the sequencer's public key ────────────────────────────
	// SealTransaction uses crypto_box_seal — the client is anonymous (no
	// sender keypair needed). The resulting ciphertext is:
	//   len(txPayload) + 48 bytes (32-byte ephemeral pubkey + 16-byte MAC).
	ciphertext, err := privacy.SealTransaction([]byte(txPayload), pubKey)
	if err != nil {
		log.Fatalf("failed to seal transaction: %v", err)
	}

	// ── Submit to sequencer ────────────────────────────────────────────────
	// The gRPC server will assign a sequence ID and return Accepted=true.
	// If this node is a Raft follower it will proxy to the leader transparently.
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
