package main

import (
	"context"
	"fmt"
	"log"

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

	// Submit transaction
	res, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
		Ciphertext: []byte("encrypted-data-from-client-1"),
	})
	if err != nil {
		log.Fatalf("RPC failed: %v", err)
	}

	fmt.Printf("Transaction accepted: %v\n", res.Accepted)
}
