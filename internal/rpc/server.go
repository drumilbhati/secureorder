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

type Server struct {
	pb.UnimplementedRPCServiceServer
	log       sequencing.OrderedLog
	proofs    *sequencing.ReceptionStore
	publisher settlement.CommitmentPublisher
	done      chan struct{}

	mu         sync.RWMutex
	proxyCache map[string]pb.RPCServiceClient
	conns      map[string]*grpc.ClientConn
}

func NewServer(log sequencing.OrderedLog) *Server {
	publisher, err := settlement.NewPublisherFromEnv()
	if err != nil {
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

func (s *Server) getProxyClient(addr string) (pb.RPCServiceClient, error) {
	s.mu.RLock()
	client, ok := s.proxyCache[addr]
	s.mu.RUnlock()
	if ok {
		return client, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double check
	if client, ok := s.proxyCache[addr]; ok {
		return client, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return nil, err
	}

	client = pb.NewRPCServiceClient(conn)
	s.proxyCache[addr] = client
	s.conns[addr] = conn

	return client, nil
}

func (s *Server) Close() {
	close(s.done)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
}

func (s *Server) PublishCommitment(ctx context.Context, commitment string) error {
	return s.publisher.PublishCommitment(ctx, commitment)
}

func (s *Server) SubmitTx(ctx context.Context, req *pb.SubmitRequest) (*pb.SubmitAck, error) {
	// If this is a Raft log, check for leadership.
	if rl, ok := s.log.(*sequencing.RaftOrderedLog); ok {
		if !rl.IsLeader() {
			leaderRaftAddr := rl.LeaderAddress()
			if leaderRaftAddr == "" {
				return nil, fmt.Errorf("no leader available in cluster")
			}

			// Derive leader gRPC address from Raft address (e.g., 172.31.40.29:7000 -> 172.31.40.29:12345)
			host, _, err := net.SplitHostPort(string(leaderRaftAddr))
			if err != nil {
				// Fallback if the address doesn't contain a port
				host = leaderRaftAddr
			}
			leaderGRPCAddr := net.JoinHostPort(host, "12345")

			leaderClient, err := s.getProxyClient(leaderGRPCAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to leader %s for proxying: %w", leaderGRPCAddr, err)
			}
			return leaderClient.SubmitTx(ctx, req)
		}
	}

	tx, err := s.log.SubmitWithReceipt(ctx, req.Ciphertext)
	if err != nil {
		return &pb.SubmitAck{Accepted: false}, fmt.Errorf("failed to submit transaction: %w", err)
	}

	s.proofs.Add(sequencing.ReceptionProof{
		SequenceID:  tx.ID,
		ArrivedUnix: tx.ArrivedAt.UnixNano(),
		Commitment:  sequencing.GenerateReceptionCommitment(tx),
	})

	return &pb.SubmitAck{Accepted: true}, nil
}

func (s *Server) ProofCount() int {
	return s.proofs.Count()
}

// Register registers the RPC server with a gRPC server.
func Register(s *grpc.Server, srv pb.RPCServiceServer) {
	pb.RegisterRPCServiceServer(s, srv)
}

// Start starts the gRPC server on the given address.
// Example address: ":50051"
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
