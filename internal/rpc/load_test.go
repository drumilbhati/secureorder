package rpc

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestLoad_ThousandConcurrentSubmissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}
	if os.Getenv("RUN_RPC_LOAD_TEST") != "1" {
		t.Skip("set RUN_RPC_LOAD_TEST=1 to run heavy 1000-client load test")
	}

	server, queue, addr := startTestServer(t)
	defer server.GracefulStop()

	const clients = 1000

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, clients)

	start := time.Now()
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()

			client := pb.NewRPCServiceClient(conn)
			resp, err := client.SubmitTx(ctx, &pb.SubmitRequest{Ciphertext: []byte(fmt.Sprintf("c-%d", id))})
			if err != nil {
				errCh <- err
				return
			}
			if !resp.Accepted {
				errCh <- fmt.Errorf("request %d not accepted", id)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("load submission failed: %v", err)
	}

	if queue.Len() != clients {
		t.Fatalf("expected queue length %d, got %d", clients, queue.Len())
	}

	t.Logf("1000 concurrent submissions completed in %s", time.Since(start))
}
