package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/drumilbhati/secureorder/internal/rpc"
	"github.com/drumilbhati/secureorder/pkg/privacy"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
	"google.golang.org/grpc"
)

func parseRaftPeers(spec string) ([]sequencing.RaftPeer, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	parts := strings.Split(spec, ",")
	peers := make([]sequencing.RaftPeer, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		fields := strings.SplitN(part, "=", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid raft peer %q, expected nodeID=host:port", part)
		}

		peers = append(peers, sequencing.RaftPeer{
			ID:      strings.TrimSpace(fields[0]),
			Address: strings.TrimSpace(fields[1]),
		})
	}

	return peers, nil
}

func defaultRaftDataDir(raftNodeID string) string {
	return filepath.Join(".local", "raft", "data", raftNodeID)
}

func newOrderedLog(orderingMode, raftNodeID, raftBind, raftPeers, raftDataDir string, raftBootstrap bool) (sequencing.OrderedLog, error) {
	switch orderingMode {
	case "local":
		return sequencing.NewTxQueue(100), nil
	case "raft":
		peers, err := parseRaftPeers(raftPeers)
		if err != nil {
			return nil, err
		}

		return sequencing.NewRaftOrderedLog(sequencing.RaftOrderedLogConfig{
			NodeID:      raftNodeID,
			BindAddress: raftBind,
			Bootstrap:   raftBootstrap,
			Peers:       peers,
			DataDir:     raftDataDir,
		})
	default:
		return nil, fmt.Errorf("unsupported ordering mode %q", orderingMode)
	}
}

func logStartupConfiguration(orderingMode, grpcAddr, raftNodeID, raftBind, raftDataDir string, raftBootstrap bool, peers []sequencing.RaftPeer) {
	fmt.Printf("Ordering backend: %s\n", orderingMode)
	fmt.Printf("gRPC bind: %s\n", grpcAddr)

	if orderingMode != "raft" {
		return
	}

	fmt.Printf("Raft node ID: %s\n", raftNodeID)
	fmt.Printf("Raft bind: %s\n", raftBind)
	fmt.Printf("Raft data dir: %s\n", raftDataDir)
	fmt.Printf("Raft bootstrap: %v\n", raftBootstrap)
	if len(peers) == 0 {
		fmt.Println("Raft peers: none configured")
		return
	}

	fmt.Println("Raft peers:")
	for _, peer := range peers {
		fmt.Printf("  - %s=%s\n", peer.ID, peer.Address)
	}
}

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
	orderingMode := flag.String("ordering", "local", "ordering backend: local or raft")
	grpcAddr := flag.String("grpc-addr", ":12345", "client-facing gRPC bind address")
	raftNodeID := flag.String("raft-node-id", "node-1", "Raft node ID")
	raftBind := flag.String("raft-bind", "127.0.0.1:7000", "Raft bind address")
	raftDataDir := flag.String("raft-data-dir", "", "Raft data directory for persistent state")
	raftBootstrap := flag.Bool("raft-bootstrap", false, "bootstrap a new Raft cluster on this node")
	raftPeers := flag.String("raft-peers", "", "comma-separated raft peers as nodeID=host:port")
	leaderLease := flag.Duration("leader-lease", 0, "leadership lease time (e.g. 60s). If > 0, leader will step down after this time.")
	publisherType := flag.String("publisher-type", "local", "settlement publisher type: local or evm")
	flag.Parse()

	if err := privacy.Init(); err != nil {
		log.Fatalf("privacy init failed: %v", err)
	}

	pubKey, secKey, err := loadOrCreateSequencerKeys()
	if err != nil {
		log.Fatalf("sequencer key setup failed: %v", err)
	}

	peers, err := parseRaftPeers(*raftPeers)
	if err != nil {
		log.Fatalf("invalid raft peer configuration: %v", err)
	}

	if *raftDataDir == "" {
		*raftDataDir = defaultRaftDataDir(*raftNodeID)
	}

	fmt.Println("Sequencer keys ready in keys/")
	fmt.Printf("Publisher type: %s\n", *publisherType)
	logStartupConfiguration(*orderingMode, *grpcAddr, *raftNodeID, *raftBind, *raftDataDir, *raftBootstrap, peers)

	orderedLog, err := newOrderedLog(*orderingMode, *raftNodeID, *raftBind, *raftPeers, *raftDataDir, *raftBootstrap)
	if err != nil {
		log.Fatalf("failed to initialize ordering backend: %v", err)
	}
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
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			batchSize := 1000
			batch := mempool.DrainWait(ctx, batchSize)
			if len(batch) == 0 {
				continue
			}

			fmt.Printf("Processing batch of %d transactions...\n", len(batch))

			ciphertexts := make([][]byte, len(batch))
			for i := range batch {
				ciphertexts[i] = batch[i].Ciphertext
			}

			// 1. Generate Batch Commitment for Settlement
			batchCommitment := sequencing.GenerateBatchCommitment(batch)
			fmt.Printf("Batch commitment: %s\n", batchCommitment)

			// 2. Publish to EVM (if leader and publisher is configured)
			if rl, ok := orderedLog.(*sequencing.RaftOrderedLog); !ok || rl.IsLeader() {
				settleCtx, settleCancel := context.WithTimeout(context.Background(), 15*time.Second)
				if err := rpcServer.PublishCommitment(settleCtx, batchCommitment); err != nil {
					fmt.Printf("failed to publish batch commitment: %v\n", err)
				} else {
					fmt.Printf("Successfully published batch commitment to EVM\n")
				}
				settleCancel()
			}

			// 3. Decrypt and Process
			plaintexts, batchErr := privacy.DecryptBatch(ciphertexts, pubKey, secKey)
			if batchErr == nil {
				fmt.Printf("Successfully decrypted batch of %d\n", len(batch))
				continue
			}

			log.Printf("batch decrypt failed, falling back to per-item decrypt: %v", batchErr)
			for _, tx := range batch {
				_, decErr := privacy.DecryptSingle(tx.Ciphertext, pubKey, secKey)
				if decErr != nil {
					log.Printf("failed to decrypt tx ID=%d: %v", tx.ID, decErr)
					continue
				}
			}
		}
	}()

	if *leaderLease > 0 && *orderingMode == "raft" {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()

			var leaderStartTime time.Time
			wasLeader := false

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					rl, ok := orderedLog.(*sequencing.RaftOrderedLog)
					if !ok {
						continue
					}

					isLeader := rl.IsLeader()
					if isLeader && !wasLeader {
						// Newly elected leader
						leaderStartTime = time.Now()
						fmt.Printf("Node %s elected leader! Lease expires in %s\n", *raftNodeID, leaderLease.String())
					}

					if isLeader && time.Since(leaderStartTime) > *leaderLease {
						fmt.Printf("Node %s lease expired, relinquishing leadership...\n", *raftNodeID)
						_ = rl.StepDown()
						wasLeader = false // Reset
						continue
					}

					wasLeader = isLeader
				}
			}
		}()
	}

	grpcServer := grpc.NewServer()
	rpcServer := rpc.NewServer(orderedLog)
	rpc.Register(grpcServer, rpcServer)

	lis, err := net.Listen("tcp", *grpcAddr)
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
	rpcServer.Close()
	grpcServer.GracefulStop()
	orderedLog.Close()
	fmt.Println("Server stopped")
}
