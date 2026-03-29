package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/drumilbhati/secureorder/pkg/sequencing"
	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// startTestServer starts a gRPC server on a random available port
// and returns the server, queue, and address.
func startTestServer(t *testing.T) (*grpc.Server, *sequencing.TxQueue, string) {
	t.Helper()

	// Find an available port
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	queue := sequencing.NewTxQueue(100)
	grpcServer := grpc.NewServer()
	rpcServer := NewServer(queue)
	Register(grpcServer, rpcServer)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Logf("serve error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	return grpcServer, queue, lis.Addr().String()
}

func TestIntegration_SingleClient(t *testing.T) {
	server, queue, addr := startTestServer(t)
	defer server.GracefulStop()

	// Create client connection
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRPCServiceClient(conn)

	// Submit a transaction
	resp, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
		Ciphertext: []byte("test-ciphertext"),
	})
	if err != nil {
		t.Fatalf("SubmitTx failed: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected accepted=true")
	}

	// Verify it's in the queue
	if queue.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", queue.Len())
	}
}

func TestIntegration_MultipleClients(t *testing.T) {
	server, queue, addr := startTestServer(t)
	defer server.GracefulStop()

	const numClients = 10

	var wg sync.WaitGroup
	results := make(chan bool, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				results <- false
				return
			}
			defer conn.Close()

			client := pb.NewRPCServiceClient(conn)

			data := fmt.Sprintf("client-%d-data", clientID)
			resp, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
				Ciphertext: []byte(data),
			})
			if err != nil || !resp.Accepted {
				results <- false
				return
			}
			results <- true
		}(i)
	}

	wg.Wait()
	close(results)

	// Count successes
	successes := 0
	for ok := range results {
		if ok {
			successes++
		}
	}

	if successes != numClients {
		t.Errorf("expected %d successes, got %d", numClients, successes)
	}

	// All transactions should be in the queue
	if queue.Len() != numClients {
		t.Errorf("expected queue length %d, got %d", numClients, queue.Len())
	}
}

func TestIntegration_ServerDrain(t *testing.T) {
	server, queue, addr := startTestServer(t)
	defer server.GracefulStop()

	// Start a goroutine to drain the queue
	drained := make(chan []byte, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			txs, err := queue.DrainWait(ctx, 1)
			if err != nil {
				return
			}
			for _, tx := range txs {
				drained <- tx.Ciphertext
			}
		}
	}()

	// Connect and submit
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewRPCServiceClient(conn)

	testData := []string{"data-1", "data-2", "data-3"}
	for _, data := range testData {
		_, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
			Ciphertext: []byte(data),
		})
		if err != nil {
			t.Fatalf("SubmitTx failed: %v", err)
		}
	}

	// Collect drained transactions
	received := make(map[string]bool)
	timeout := time.After(2 * time.Second)
	for len(received) < len(testData) {
		select {
		case data := <-drained:
			received[string(data)] = true
		case <-timeout:
			t.Fatalf("timeout waiting for drained transactions, got %d/%d", len(received), len(testData))
		}
	}

	// Verify all data was received
	for _, data := range testData {
		if !received[data] {
			t.Errorf("missing drained data: %s", data)
		}
	}
}

func TestIntegration_ConcurrentSubmissions(t *testing.T) {
	server, queue, addr := startTestServer(t)
	defer server.GracefulStop()

	const (
		numClients    = 20
		submissionsPerClient = 5
	)

	var wg sync.WaitGroup
	errChan := make(chan error, numClients*submissionsPerClient)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				errChan <- fmt.Errorf("connection failed: %w", err)
				return
			}
			defer conn.Close()

			client := pb.NewRPCServiceClient(conn)

			for j := 0; j < submissionsPerClient; j++ {
				data := fmt.Sprintf("client-%d-tx-%d", clientID, j)
				resp, err := client.SubmitTx(context.Background(), &pb.SubmitRequest{
					Ciphertext: []byte(data),
				})
				if err != nil {
					errChan <- err
					return
				}
				if !resp.Accepted {
					errChan <- fmt.Errorf("transaction not accepted")
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("submission error: %v", err)
	}

	// Verify all transactions are in the queue
	expected := numClients * submissionsPerClient
	if queue.Len() != expected {
		t.Errorf("expected queue length %d, got %d", expected, queue.Len())
	}
}
