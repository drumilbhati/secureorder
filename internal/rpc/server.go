package rpc

import (
	"context"
	"fmt"
	"net"
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
}

func NewServer(log sequencing.OrderedLog) *Server {
	publisher, err := settlement.NewPublisherFromEnv()
	if err != nil {
		publisher = settlement.NoopPublisher{}
	}

	s := &Server{
		log:       log,
		proofs:    sequencing.NewReceptionStore(),
		publisher: publisher,
		done:      make(chan struct{}),
	}

	go s.runSettlementLoop()

	return s
}

func (s *Server) runSettlementLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastPublishedCommitment string

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			last, ok := s.proofs.Last()
			if !ok {
				continue
			}

			if last.Commitment == lastPublishedCommitment {
				continue
			}

			// Perform asynchronous settlement of the latest batch commitment
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if err := s.publisher.PublishCommitment(ctx, last.Commitment); err != nil {
				fmt.Printf("failed to publish background commitment: %v\n", err)
			} else {
				lastPublishedCommitment = last.Commitment
			}
			cancel()
		}
	}
}

func (s *Server) Close() {
	close(s.done)
}

func (s *Server) SubmitTx(ctx context.Context, req *pb.SubmitRequest) (*pb.SubmitAck, error) {
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
