package sequencing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

const defaultRaftApplyTimeout = 10 * time.Second

type RaftPeer struct {
	ID      string
	Address string
}

type RaftOrderedLogConfig struct {
	NodeID       string
	BindAddress  string
	Bootstrap    bool
	Peers        []RaftPeer
	CommitBuffer int
	DataDir      string
}

type raftProposal struct {
	SubmittedAtUnixNano int64  `json:"submitted_at_unix_nano"`
	Ciphertext          []byte `json:"ciphertext"`
}

// RaftOrderedLog backs OrderedLog with a hashicorp/raft replicated log.
type RaftOrderedLog struct {
	raft      *raft.Raft
	transport *raft.NetworkTransport

	committed chan EncryptedTransaction
	done      chan struct{}
	closeOnce sync.Once
}

var _ OrderedLog = (*RaftOrderedLog)(nil)

func NewRaftOrderedLog(cfg RaftOrderedLogConfig) (*RaftOrderedLog, error) {
	if strings.TrimSpace(cfg.NodeID) == "" {
		return nil, errors.New("raft node ID is required")
	}
	if strings.TrimSpace(cfg.BindAddress) == "" {
		return nil, errors.New("raft bind address is required")
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("raft data directory is required")
	}
	if cfg.CommitBuffer <= 0 {
		cfg.CommitBuffer = 1024
	}

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve raft bind address: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddress, addr, 3, 10*time.Second, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("create raft transport: %w", err)
	}

	committed := make(chan EncryptedTransaction, cfg.CommitBuffer)
	done := make(chan struct{})

	fsm := &raftFSM{
		committed: committed,
		done:      done,
	}

	raftConfig := raft.DefaultConfig()
	raftConfig.LocalID = raft.ServerID(cfg.NodeID)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft data dir: %w", err)
	}

	logStorePath := filepath.Join(cfg.DataDir, "raft-log.bolt")
	stableStorePath := filepath.Join(cfg.DataDir, "raft-stable.bolt")
	snapshotDir := filepath.Join(cfg.DataDir, "snapshots")

	logStore, err := raftboltdb.NewBoltStore(logStorePath)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft log store: %w", err)
	}

	stableStore, err := raftboltdb.NewBoltStore(stableStorePath)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft stable store: %w", err)
	}

	snapshotStore, err := raft.NewFileSnapshotStore(snapshotDir, 2, os.Stderr)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft snapshot store: %w", err)
	}

	node, err := raft.NewRaft(raftConfig, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft node: %w", err)
	}

	orderedLog := &RaftOrderedLog{
		raft:      node,
		transport: transport,
		committed: committed,
		done:      done,
	}

	if cfg.Bootstrap {
		servers := make([]raft.Server, 0, len(cfg.Peers)+1)
		seenSelf := false
		localAddr := transport.LocalAddr()

		for _, peer := range cfg.Peers {
			if strings.TrimSpace(peer.ID) == "" || strings.TrimSpace(peer.Address) == "" {
				continue
			}
			server := raft.Server{
				ID:      raft.ServerID(peer.ID),
				Address: raft.ServerAddress(peer.Address),
			}
			servers = append(servers, server)
			if peer.ID == cfg.NodeID {
				seenSelf = true
			}
		}

		if !seenSelf {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(cfg.NodeID),
				Address: localAddr,
			})
		}

		if len(servers) == 0 {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(cfg.NodeID),
				Address: localAddr,
			})
		}

		bootstrap := node.BootstrapCluster(raft.Configuration{Servers: servers})
		if err := bootstrap.Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			orderedLog.Close()
			return nil, fmt.Errorf("bootstrap raft cluster: %w", err)
		}
	}

	return orderedLog, nil
}

func (r *RaftOrderedLog) SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedTransaction{}, err
	}

	payload := make([]byte, len(ciphertext))
	copy(payload, ciphertext)

	proposalBytes, err := json.Marshal(raftProposal{
		SubmittedAtUnixNano: time.Now().UnixNano(),
		Ciphertext:          payload,
	})
	if err != nil {
		return EncryptedTransaction{}, fmt.Errorf("marshal raft proposal: %w", err)
	}

	timeout := defaultRaftApplyTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return EncryptedTransaction{}, ctx.Err()
		}
	}

	future := r.raft.Apply(proposalBytes, timeout)
	if err := future.Error(); err != nil {
		return EncryptedTransaction{}, fmt.Errorf("raft apply failed: %w", err)
	}

	tx, ok := future.Response().(EncryptedTransaction)
	if !ok {
		return EncryptedTransaction{}, errors.New("raft apply returned unexpected response type")
	}
	return tx, nil
}

func (r *RaftOrderedLog) IsLeader() bool {
	return r.raft.State() == raft.Leader
}

func (r *RaftOrderedLog) LeaderAddress() string {
	addr, _ := r.raft.LeaderWithID()
	return string(addr)
}

func (r *RaftOrderedLog) AddVoter(id, address string) error {
	future := r.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, 0)
	return future.Error()
}

func (r *RaftOrderedLog) StepDown() error {
	if !r.IsLeader() {
		return nil
	}
	fmt.Println("Leader lease expired, stepping down...")
	return r.raft.LeadershipTransfer().Error()
}

func (r *RaftOrderedLog) DrainWait(ctx context.Context, batchSize int) ([]EncryptedTransaction, error) {
	if batchSize <= 0 {
		return nil, nil
	}

	batch := make([]EncryptedTransaction, 0, batchSize)
	for len(batch) < batchSize {
		select {
		case tx := <-r.committed:
			batch = append(batch, tx)
		case <-r.done:
			return batch, ErrQueueClosed
		case <-ctx.Done():
			return batch, ctx.Err()
		}
	}
	return batch, nil
}

func (r *RaftOrderedLog) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
		if r.raft != nil {
			_ = r.raft.Shutdown().Error()
		}
		if r.transport != nil {
			_ = r.transport.Close()
		}
	})
}

type raftFSM struct {
	committed chan<- EncryptedTransaction
	done      <-chan struct{}
}

func (f *raftFSM) Apply(log *raft.Log) interface{} {
	var proposal raftProposal
	if err := json.Unmarshal(log.Data, &proposal); err != nil {
		return err
	}

	tx := EncryptedTransaction{
		ID:         log.Index,
		Ciphertext: append([]byte(nil), proposal.Ciphertext...),
		ArrivedAt:  time.Unix(0, proposal.SubmittedAtUnixNano),
	}

	select {
	case f.committed <- tx:
	case <-f.done:
	}

	return tx
}

func (f *raftFSM) Snapshot() (raft.FSMSnapshot, error) {
	return noopRaftSnapshot{}, nil
}

func (f *raftFSM) Restore(io.ReadCloser) error {
	return nil
}

type noopRaftSnapshot struct{}

func (noopRaftSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Close()
}

func (noopRaftSnapshot) Release() {}
