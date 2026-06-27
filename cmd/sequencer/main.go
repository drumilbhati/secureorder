// Package main is the entry point for the Secure-Order sequencer node.
//
// The sequencer is the core component of the MEV-mitigation pipeline.
// Its responsibilities are:
//  1. Accept encrypted transactions from clients over gRPC.
//  2. Assign each transaction an immutable sequence ID via Raft consensus
//     (or a local in-memory queue in single-node mode).
//  3. Hold transactions in an encrypted mempool — their contents remain
//     hidden until a full batch is ready.
//  4. Reveal the batch by decrypting it via the C++ privacy layer.
//  5. Publish a cryptographic commitment of the batch's order to the
//     on-chain OrderVerifier smart contract.
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
	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
)

// parseRaftPeers parses a comma-separated peer specification of the form:
//
//	nodeID=host:port,nodeID2=host2:port2
//
// This is used to build the initial Raft cluster membership at bootstrap time.
// Returns nil (no error) for an empty string — single-node clusters are valid.
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

		// Each entry must be exactly "nodeID=host:port"
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

// defaultRaftDataDir returns a per-node data directory path under .local/raft/data/.
// Using the node ID in the path means multiple nodes on the same machine
// (e.g. in a local test cluster) each get isolated storage.
func defaultRaftDataDir(raftNodeID string) string {
	return filepath.Join(".local", "raft", "data", raftNodeID)
}

// newOrderedLog constructs the ordering backend based on the selected mode.
//
//   - "local" — single-node in-memory TxQueue; no consensus, no persistence.
//     Useful for development and testing.
//   - "raft"  — replicated log backed by Hashicorp Raft. Provides consensus-
//     guaranteed FIFO ordering across a cluster of sequencer nodes.
func newOrderedLog(orderingMode, raftNodeID, raftBind, raftPeers, raftDataDir string, raftBootstrap bool) (sequencing.OrderedLog, error) {
	switch orderingMode {
	case "local":
		// Buffer up to 100 transactions before backpressure kicks in.
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

// logStartupConfiguration prints the active configuration to stdout at startup.
// Raft-specific fields are omitted when running in local mode to reduce noise.
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

// pathExists returns true if path exists on disk (file or directory).
// Used to detect whether a Raft node has prior persistent state.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// attemptJoin dials an existing cluster node over gRPC and asks it to add this
// node to the Raft voter set. The target node will proxy the request to the
// current Raft leader if it isn't the leader itself.
//
// This is called only on fresh nodes (no existing raft-log.bolt). Restarting
// nodes skip the join step because their existing Raft state already encodes
// their cluster membership.
func attemptJoin(joinAddr, nodeID, raftAddr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, joinAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return fmt.Errorf("dial join address: %w", err)
	}
	defer conn.Close()

	client := pb.NewRPCServiceClient(conn)
	resp, err := client.JoinCluster(ctx, &pb.JoinRequest{
		NodeId:      nodeID,
		RaftAddress: raftAddr,
	})
	if err != nil {
		return fmt.Errorf("join cluster rpc: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("join cluster failed: %s", resp.ErrorMessage)
	}

	fmt.Println("Successfully joined cluster")
	return nil
}

// loadOrCreateSequencerKeys returns the sequencer's Curve25519 keypair.
//
// On first startup the keys are generated and persisted under keys/.
// On subsequent startups the persisted keys are reloaded from disk.
//
// Key persistence matters because clients encrypt transactions to the
// sequencer's public key. If the key changed between restarts, all
// in-flight transactions encrypted to the old key would fail to decrypt.
func loadOrCreateSequencerKeys() ([]byte, []byte, error) {
	// 0700: only the sequencer process should be able to read the private key.
	if err := os.MkdirAll("keys", 0o700); err != nil {
		return nil, nil, fmt.Errorf("failed to create key directory: %w", err)
	}

	const pubPath = "keys/sequencer_public.key"
	const secPath = "keys/sequencer_private.key"

	// If both files exist and are readable, reuse the existing keypair.
	pub, pubErr := privacy.LoadKeyFromFile(pubPath, privacy.PublicKeyBytes)
	sec, secErr := privacy.LoadKeyFromFile(secPath, privacy.SecretKeyBytes)
	if pubErr == nil && secErr == nil {
		return pub, sec, nil
	}

	// At least one key is missing — generate a fresh keypair.
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
	// ── CLI flags ──────────────────────────────────────────────────────────
	orderingMode := flag.String("ordering", "local", "ordering backend: local or raft")
	grpcAddr := flag.String("grpc-addr", ":12345", "client-facing gRPC bind address")
	raftNodeID := flag.String("raft-node-id", "node-1", "Raft node ID")
	raftBind := flag.String("raft-bind", "127.0.0.1:7000", "Raft bind address")
	raftDataDir := flag.String("raft-data-dir", "", "Raft data directory for persistent state")
	raftBootstrap := flag.Bool("raft-bootstrap", false, "bootstrap a new Raft cluster on this node")
	raftPeers := flag.String("raft-peers", "", "comma-separated raft peers as nodeID=host:port")
	raftJoinAddr := flag.String("raft-join-addr", "", "existing cluster gRPC address to join")
	leaderLease := flag.Duration("leader-lease", 0, "leadership lease time (e.g. 60s). If > 0, leader will step down after this time.")
	publisherType := flag.String("publisher-type", "local", "settlement publisher type: local or evm")
	flag.Parse()

	// ── Privacy layer initialisation ──────────────────────────────────────
	// Must be called before any encrypt/decrypt operations.
	// Internally calls sodium_init() which seeds the CSPRNG.
	if err := privacy.Init(); err != nil {
		log.Fatalf("privacy init failed: %v", err)
	}

	// ── Sequencer keypair ─────────────────────────────────────────────────
	// pubKey is distributed to clients (via keys/sequencer_public.key) so
	// they can seal their transactions. secKey never leaves this process.
	pubKey, secKey, err := loadOrCreateSequencerKeys()
	if err != nil {
		log.Fatalf("sequencer key setup failed: %v", err)
	}

	peers, err := parseRaftPeers(*raftPeers)
	if err != nil {
		log.Fatalf("invalid raft peer configuration: %v", err)
	}

	// Fall back to the per-node default data directory if none is provided.
	if *raftDataDir == "" {
		*raftDataDir = defaultRaftDataDir(*raftNodeID)
	}

	// ── Dynamic cluster join ───────────────────────────────────────────────
	// A fresh Raft node (no existing bolt log) must be added to the cluster's
	// voter set by the leader before it can participate in consensus.
	// Restarting nodes already have their membership encoded in their Raft log,
	// so we skip the join to avoid conflicting with the existing entry.
	if *orderingMode == "raft" && !*raftBootstrap && *raftJoinAddr != "" {
		if !pathExists(filepath.Join(*raftDataDir, "raft-log.bolt")) {
			fmt.Printf("Fresh node detected. Attempting to join cluster via %s...\n", *raftJoinAddr)
			if err := attemptJoin(*raftJoinAddr, *raftNodeID, *raftBind); err != nil {
				log.Fatalf("failed to join cluster: %v", err)
			}
		} else {
			fmt.Println("Local Raft state found, skipping dynamic join.")
		}
	}

	fmt.Println("Sequencer keys ready in keys/")
	fmt.Printf("Publisher type: %s\n", *publisherType)
	logStartupConfiguration(*orderingMode, *grpcAddr, *raftNodeID, *raftBind, *raftDataDir, *raftBootstrap, peers)

	// ── Ordering backend ───────────────────────────────────────────────────
	orderedLog, err := newOrderedLog(*orderingMode, *raftNodeID, *raftBind, *raftPeers, *raftDataDir, *raftBootstrap)
	if err != nil {
		log.Fatalf("failed to initialize ordering backend: %v", err)
	}

	// ── Encrypted mempool ──────────────────────────────────────────────────
	// Sits between the ordered log and the batch-decrypt stage.
	// Transactions are held encrypted here until a full batch is assembled,
	// ensuring no actor can observe transaction contents before ordering is final.
	mempool := sequencing.NewEncryptedMempool(1024)

	// ── RPC server ────────────────────────────────────────────────────────
	// Initialise early so the batch-processing goroutine below can call
	// rpcServer.PublishCommitment() once it is ready.
	rpcServer := rpc.NewServer(orderedLog)

	ctx, cancel := context.WithCancel(context.Background())

	// ── Ingress collector goroutine ────────────────────────────────────────
	// Continuously drains committed transactions from the ordered log and
	// places them into the encrypted mempool one at a time.
	// This keeps the ordering stage (Raft) decoupled from the reveal stage
	// (batch decrypt), allowing each to run at its own pace.
	go func() {
		for {
			txs, err := orderedLog.DrainWait(ctx, 1)
			if err != nil {
				// Context cancelled — clean shutdown.
				return
			}
			for _, tx := range txs {
				mempool.Add(tx)
			}
		}
	}()

	// ── Batch processing goroutine ─────────────────────────────────────────
	// This is the reveal phase of the commit-reveal scheme:
	//  1. Wait for batchSize encrypted transactions to accumulate.
	//  2. Compute a batch commitment (SHA-256 Merkle-style root hash).
	//  3. Publish that commitment to the on-chain OrderVerifier (leader only).
	//  4. Decrypt the batch via the C++ privacy layer.
	//  5. Fall back to per-item decryption if the batch call fails.
	go func() {
		for {
			// Check for shutdown before blocking on DrainWait.
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

			// Step 1: Generate a single root hash covering all (SeqID, timestamp,
			// ciphertext) tuples in this batch. This is the on-chain proof of order.
			batchCommitment := sequencing.GenerateBatchCommitment(batch)
			fmt.Printf("Batch commitment: %s\n", batchCommitment)

			// Step 2: Only the Raft leader publishes to the EVM. Followers would
			// produce duplicate commitOrder transactions for the same batch.
			// The settlement runs asynchronously so EVM latency doesn't stall the
			// next batch from being processed.
			if rl, ok := orderedLog.(*sequencing.RaftOrderedLog); !ok || rl.IsLeader() {
				go func(commitment string) {
					settleCtx, settleCancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer settleCancel()

					if err := rpcServer.PublishCommitment(settleCtx, commitment); err != nil {
						fmt.Printf("failed to publish batch commitment: %v\n", err)
					} else {
						fmt.Printf("Successfully published batch commitment to EVM: %s\n", commitment)
					}
				}(batchCommitment)
			}

			// Step 3: Decrypt the entire batch in one parallel C++ call.
			// If batch decryption fails (e.g. one corrupted ciphertext poisons
			// the call), fall back to per-item decryption so good transactions
			// are not silently dropped.
			_, batchErr := privacy.DecryptBatch(ciphertexts, pubKey, secKey)
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

	// ── Leader lease goroutine (optional) ─────────────────────────────────
	// When -leader-lease is set, a node voluntarily relinquishes leadership
	// after holding it for the configured duration. This enables round-robin
	// leader rotation in a cluster, preventing a single node from sequencing
	// all transactions indefinitely.
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
						// Transition: follower → leader. Record when leadership began.
						leaderStartTime = time.Now()
						fmt.Printf("Node %s elected leader! Lease expires in %s\n", *raftNodeID, leaderLease.String())
					}

					// Lease expired — trigger a leadership transfer to a healthy follower.
					if isLeader && time.Since(leaderStartTime) > *leaderLease {
						fmt.Printf("Node %s lease expired, relinquishing leadership...\n", *raftNodeID)
						_ = rl.StepDown()
						wasLeader = false
						continue
					}

					wasLeader = isLeader
				}
			}
		}()
	}

	// ── gRPC server ────────────────────────────────────────────────────────
	grpcServer := grpc.NewServer()
	rpc.Register(grpcServer, rpcServer)

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// Serve in a goroutine so main can block on the quit signal below.
	go func() {
		fmt.Printf("gRPC server listening on %s\n", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("serve error: %v", err)
		}
	}()

	// ── Graceful shutdown ──────────────────────────────────────────────────
	// Block until SIGINT (Ctrl-C) or SIGTERM (from the OS / Kubernetes).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	fmt.Println("Shutting down...")
	// 1. Cancel the context — stops the ingress collector and batch processor.
	cancel()
	// 2. Close EVM publisher connections.
	rpcServer.Close()
	// 3. Wait for in-flight RPCs to finish before stopping the listener.
	grpcServer.GracefulStop()
	// 4. Flush and close the Raft log last — after all writers have stopped.
	orderedLog.Close()
	fmt.Println("Server stopped")
}
