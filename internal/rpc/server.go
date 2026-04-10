package rpc

import (
	"context"
	"fmt"
	"net"

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
}

func NewServer(log sequencing.OrderedLog) *Server {
	publisher, err := settlement.NewPublisherFromEnv()
	if err != nil {
		publisher = settlement.NoopPublisher{}
	}

	return &Server{
		log:       log,
		proofs:    sequencing.NewReceptionStore(),
		publisher: publisher,
	}
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

	if last, ok := s.proofs.Last(); ok {
		_ = s.publisher.PublishCommitment(ctx, last.Commitment)
	}

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
