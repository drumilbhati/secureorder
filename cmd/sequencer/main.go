package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
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

func newOrderedLog(orderingMode, raftNodeID, raftBind, raftPeers string, raftBootstrap bool) (sequencing.OrderedLog, error) {
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
		})
	default:
		return nil, fmt.Errorf("unsupported ordering mode %q", orderingMode)
	}
}

func logStartupConfiguration(orderingMode, grpcAddr, raftNodeID, raftBind string, raftBootstrap bool, peers []sequencing.RaftPeer) {
	fmt.Printf("Ordering backend: %s\n", orderingMode)
	fmt.Printf("gRPC bind: %s\n", grpcAddr)

	if orderingMode != "raft" {
		return
	}

	fmt.Printf("Raft node ID: %s\n", raftNodeID)
	fmt.Printf("Raft bind: %s\n", raftBind)
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
	raftBootstrap := flag.Bool("raft-bootstrap", false, "bootstrap a new Raft cluster on this node")
	raftPeers := flag.String("raft-peers", "", "comma-separated raft peers as nodeID=host:port")
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

	fmt.Println("Sequencer keys ready in keys/")
	logStartupConfiguration(*orderingMode, *grpcAddr, *raftNodeID, *raftBind, *raftBootstrap, peers)

	orderedLog, err := newOrderedLog(*orderingMode, *raftNodeID, *raftBind, *raftPeers, *raftBootstrap)
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
	grpcServer.GracefulStop()
	orderedLog.Close()
	fmt.Println("Server stopped")
}
