package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/drumilbhati/secureorder/internal/rpc"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
	"google.golang.org/grpc"
)

func main() {
	// Create the transaction queue
	queue := sequencing.NewTxQueue(100)

	// Start a goroutine to drain the queue (otherwise it will fill up)
	go func() {
		for {
			txs, err := queue.DrainWait(context.Background(), 10)
			if err != nil {
				return // Context cancelled or queue closed
			}
			for _, tx := range txs {
				fmt.Printf("Processed tx ID=%d, size=%d bytes\n", tx.ID, len(tx.Ciphertext))
			}
		}
	}()

	// Create gRPC server
	grpcServer := grpc.NewServer()
	rpcServer := rpc.NewServer(queue)
	rpc.Register(grpcServer, rpcServer)

	// Start listening
	lis, err := net.Listen("tcp", ":12345")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Handle graceful shutdown
	go func() {
		fmt.Printf("gRPC server listening on %s\n", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("serve error: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	fmt.Println("Shutting down...")

	grpcServer.GracefulStop()
	queue.Close()
	fmt.Println("Server stopped")
}