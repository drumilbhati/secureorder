// Package rpc implements the gRPC server that sits in front of the Raft cluster.
//
// Any node in the cluster can receive a client RPC. If the receiving node is
// not the current Raft leader, it transparently proxies the request to the
// leader using a cached gRPC connection. This means clients never need to know
// which node is the leader — they can target any node.
package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/drumilbhati/secureorder/internal/settlement"
	"github.com/drumilbhati/secureorder/pkg/sequencing"
	pb "github.com/drumilbhati/secureorder/proto"
	"google.golang.org/grpc"
)

// Server implements pb.RPCServiceServer and wraps the sequencing and settlement
// subsystems behind a single gRPC endpoint.
type Server struct {
	// Embed the unimplemented stub so the struct satisfies the interface even
	// if new RPC methods are added to the proto in the future.
	pb.UnimplementedRPCServiceServer

	// log is the ordering backend — either a single-node TxQueue or a Raft log.
	log sequencing.OrderedLog

	// proofs stores per-transaction reception commitments in arrival order.
	// These can be used to audit the sequencer's claimed ordering.
	proofs *sequencing.ReceptionStore

	// publisher sends batch commitments to the on-chain OrderVerifier contract.
	// Falls back to NoopPublisher when EVM env vars are not set.
	publisher settlement.CommitmentPublisher

	// done is closed during Close() to signal background work to stop.
	done chan struct{}

	// mu guards proxyCache and conns — both are written lazily on first use.
	mu         sync.RWMutex
	proxyCache map[string]pb.RPCServiceClient // addr → cached gRPC client
	conns      map[string]*grpc.ClientConn    // addr → underlying connection (for cleanup)
}

// NewServer constructs a Server with the given ordering backend.
//
// The EVM publisher is created from environment variables if they are all
// present (ORDER_VERIFIER_RPC_URL, ORDER_VERIFIER_CONTRACT,
// ORDER_VERIFIER_PRIVATE_KEY, ORDER_VERIFIER_CHAIN_ID). If any variable is
// missing the server falls back to a no-op publisher so the sequencer can
// run without EVM access.
func NewServer(log sequencing.OrderedLog) *Server {
	publisher, err := settlement.NewPublisherFromEnv()
	if err != nil {
		// EVM publisher misconfigured — degrade gracefully rather than crash.
		publisher = settlement.NoopPublisher{}
	}

	s := &Server{
		log:        log,
		proofs:     sequencing.NewReceptionStore(),
		publisher:  publisher,
		done:       make(chan struct{}),
		proxyCache: make(map[string]pb.RPCServiceClient),
		conns:      make(map[string]*grpc.ClientConn),
	}

	return s
}

// getProxyClient returns a cached gRPC client for addr, dialing it if this is
// the first request to that address. The cache is populated lazily using a
// double-checked locking pattern:
//
//  1. Acquire a read lock and check the cache — the common case (cache hit)
//     acquires the cheaper read lock and returns immediately.
//  2. On a cache miss, promote to a write lock and check again before dialing,
//     because another goroutine may have populated the entry between steps 1 and 2.
func (s *Server) getProxyClient(addr string) (pb.RPCServiceClient, error) {
	// Fast path: cache hit under read lock.
	s.mu.RLock()
	client, ok := s.proxyCache[addr]
	s.mu.RUnlock()
	if ok {
		return client, nil
	}

	// Slow path: must dial — acquire write lock.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check: another goroutine may have dialed while we waited for the write lock.
	if client, ok := s.proxyCache[addr]; ok {
		return client, nil
	}

	// Dial with a 5-second timeout so a slow or unreachable leader doesn't
	// block the caller's RPC indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, err
	}

	client = pb.NewRPCServiceClient(conn)
	s.proxyCache[addr] = client
	s.conns[addr] = conn // keep the conn so we can close it in Close()

	return client, nil
}

// Close signals shutdown and closes all cached gRPC connections to other nodes.
func (s *Server) Close() {
	close(s.done)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
}

// PublishCommitment forwards a batch commitment hex string to the settlement
// publisher. Called by the batch-processing goroutine in cmd/sequencer/main.go.
func (s *Server) PublishCommitment(ctx context.Context, commitment string) error {
	return s.publisher.PublishCommitment(ctx, commitment)
}

// SubmitTx is the primary client-facing RPC. It accepts a sealed (encrypted)
// transaction and returns an acknowledgement with the assigned sequence ID.
//
// Leader forwarding: if this node is a Raft follower, the request is proxied
// to the current leader. Only the leader can call raft.Apply(), so all
// submissions must ultimately be processed by the leader.
//
// The leader derives the gRPC port of other nodes by replacing their Raft
// port with the hardcoded gRPC port 12345. This is a known limitation of the
// current implementation.
func (s *Server) SubmitTx(ctx context.Context, req *pb.SubmitRequest) (*pb.SubmitAck, error) {
	// Check Raft leadership only when using the Raft backend.
	if rl, ok := s.log.(*sequencing.RaftOrderedLog); ok {
		if !rl.IsLeader() {
			leaderRaftAddr := rl.LeaderAddress()
			if leaderRaftAddr == "" {
				return nil, fmt.Errorf("no leader available in cluster")
			}

			// Derive leader gRPC address from its Raft address.
			// e.g. 172.31.40.29:7000 → 172.31.40.29:12345
			host, _, err := net.SplitHostPort(string(leaderRaftAddr))
			if err != nil {
				// Address has no port (unusual) — use as-is.
				host = leaderRaftAddr
			}
			leaderGRPCAddr := net.JoinHostPort(host, "12345")

			leaderClient, err := s.getProxyClient(leaderGRPCAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to leader %s for proxying: %w", leaderGRPCAddr, err)
			}
			// Proxy the original request unchanged — the leader will assign the SeqID.
			return leaderClient.SubmitTx(ctx, req)
		}
	}

	// This node is the leader (or using local ordering) — commit the transaction.
	tx, err := s.log.SubmitWithReceipt(ctx, req.Ciphertext)
	if err != nil {
		return &pb.SubmitAck{Accepted: false}, fmt.Errorf("failed to submit transaction: %w", err)
	}

	// Persist a proof-of-reception commitment: (SeqID, arrival timestamp,
	// SHA-256 of the ciphertext). This is stored in-memory and can be
	// queried to audit the sequencer's claimed ordering.
	s.proofs.Add(sequencing.ReceptionProof{
		SequenceID:  tx.ID,
		ArrivedUnix: tx.ArrivedAt.UnixNano(),
		Commitment:  sequencing.GenerateReceptionCommitment(tx),
	})

	return &pb.SubmitAck{Accepted: true}, nil
}

// JoinCluster is called by a new node that wants to join the Raft consensus
// group. If this node is the leader it calls raft.AddVoter() directly.
// Otherwise it proxies the request to the leader using the same mechanism as
// SubmitTx.
func (s *Server) JoinCluster(ctx context.Context, req *pb.JoinRequest) (*pb.JoinResponse, error) {
	rl, ok := s.log.(*sequencing.RaftOrderedLog)
	if !ok {
		// Local (non-Raft) mode does not support dynamic cluster membership.
		return &pb.JoinResponse{
			Success:      false,
			ErrorMessage: "ordering backend does not support dynamic joins",
		}, nil
	}

	if !rl.IsLeader() {
		leaderRaftAddr := rl.LeaderAddress()
		if leaderRaftAddr == "" {
			return &pb.JoinResponse{
				Success:      false,
				ErrorMessage: "no leader available in cluster",
			}, nil
		}

		host, _, err := net.SplitHostPort(string(leaderRaftAddr))
		if err != nil {
			host = leaderRaftAddr
		}
		leaderGRPCAddr := net.JoinHostPort(host, "12345")

		leaderClient, err := s.getProxyClient(leaderGRPCAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to leader %s for proxying join: %w", leaderGRPCAddr, err)
		}
		return leaderClient.JoinCluster(ctx, req)
	}

	// This node is the leader — add the new node as a full voting member.
	if err := rl.AddVoter(req.NodeId, req.RaftAddress); err != nil {
		return &pb.JoinResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("failed to add voter: %v", err),
		}, nil
	}

	return &pb.JoinResponse{Success: true}, nil
}

// ProofCount returns the number of reception proofs stored since startup.
// Used in tests to verify that every submitted transaction was acknowledged.
func (s *Server) ProofCount() int {
	return s.proofs.Count()
}

// Register attaches the RPC server's handlers to a gRPC server instance.
// Separating registration from construction lets callers (e.g. cmd/sequencer)
// add interceptors or configure the gRPC server before registering.
func Register(s *grpc.Server, srv pb.RPCServiceServer) {
	pb.RegisterRPCServiceServer(s, srv)
}

// Start is a convenience function that creates a gRPC server, registers the
// RPC service, and starts serving on address. It blocks until the server stops.
// Most production code uses Register + grpc.NewServer directly for more control.
func Start(address string, log sequencing.OrderedLog) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	server := grpc.NewServer()
	rpcServer := NewServer(log)
	pb.RegisterRPCServiceServer(server, rpcServer)

	fmt.Printf("gRPC server listening on %s\n", address)
	if err := server.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}
