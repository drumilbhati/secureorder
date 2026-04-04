package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "localhost:12345", "gRPC sequencer address")
	clients := flag.Int("clients", 1000, "number of concurrent clients")
	requestsPerClient := flag.Int("requests", 1, "requests per client")
	timeout := flag.Duration("timeout", 20*time.Second, "overall timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	start := time.Now()
	var success atomic.Int64
	var failed atomic.Int64

	var wg sync.WaitGroup
	wg.Add(*clients)

	for i := 0; i < *clients; i++ {
		go func(clientID int) {
			defer wg.Done()

			conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				failed.Add(int64(*requestsPerClient))
				return
			}
			defer conn.Close()

			client := pb.NewRPCServiceClient(conn)
			for j := 0; j < *requestsPerClient; j++ {
				payload := []byte(fmt.Sprintf("loadtest-client-%d-req-%d", clientID, j))
				resp, err := client.SubmitTx(ctx, &pb.SubmitRequest{Ciphertext: payload})
				if err != nil || !resp.Accepted {
					failed.Add(1)
					continue
				}
				success.Add(1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	total := int64(*clients * *requestsPerClient)
	if total == 0 {
		log.Fatal("total requests is zero")
	}

	rps := float64(success.Load()) / elapsed.Seconds()
	fmt.Println("========================================")
	fmt.Println("   SECUREORDER RPC LOAD TEST SUMMARY    ")
	fmt.Println("========================================")
	fmt.Printf("Address            : %s\n", *addr)
	fmt.Printf("Concurrent clients : %d\n", *clients)
	fmt.Printf("Requests/client    : %d\n", *requestsPerClient)
	fmt.Printf("Total requests     : %d\n", total)
	fmt.Printf("Successful         : %d\n", success.Load())
	fmt.Printf("Failed             : %d\n", failed.Load())
	fmt.Printf("Elapsed            : %s\n", elapsed)
	fmt.Printf("Throughput (RPS)   : %.2f\n", rps)
	fmt.Println("========================================")

	if failed.Load() > 0 {
		log.Fatalf("load test completed with failures: %d", failed.Load())
	}
}
