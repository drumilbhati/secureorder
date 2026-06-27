// Package sequencing implements the transaction ordering and mempool subsystems.
//
// The core abstraction is the OrderedLog interface: any backend (local queue or
// Raft cluster) that can assign a globally unique, monotonically increasing ID
// to an encrypted transaction satisfies this interface.
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

// defaultRaftApplyTimeout is the maximum time we wait for Raft to replicate and
// commit a proposal. It kicks in when the caller's context has no deadline.
const defaultRaftApplyTimeout = 10 * time.Second

// RaftPeer identifies a node in the Raft cluster by its stable ID and network address.
type RaftPeer struct {
	ID      string // stable node identifier, e.g. "node-1"
	Address string // Raft transport address, e.g. "10.0.0.1:7000"
}

// RaftOrderedLogConfig holds all parameters required to start a Raft node.
type RaftOrderedLogConfig struct {
	NodeID       string
	BindAddress  string     // TCP address that Raft will listen on
	Bootstrap    bool       // true on the first node that creates a new cluster
	Peers        []RaftPeer // initial voter set; only relevant when Bootstrap == true
	CommitBuffer int        // capacity of the committed-transaction channel
	DataDir      string     // directory for bolt stores and snapshots
}

// raftProposal is the value that every SubmitWithReceipt call serialises and
// proposes to the Raft log. It carries the ciphertext together with the
// client-side submission timestamp so the FSM can reconstruct ArrivedAt
// deterministically on every replica.
type raftProposal struct {
	SubmittedAtUnixNano int64  `json:"submitted_at_unix_nano"`
	Ciphertext          []byte `json:"ciphertext"`
}

// RaftOrderedLog backs OrderedLog with a hashicorp/raft replicated log.
//
// Every accepted transaction passes through Raft.Apply(), which replicates the
// payload to a quorum of followers before the leader commits it. The FSM's
// Apply() method converts the committed log entry into an EncryptedTransaction
// and sends it on the committed channel, which callers consume via DrainWait().
//
// This design gives the system its core ordering guarantee: two replicas will
// always see transactions in the same Raft index order, so the sequence ID
// (= log.Index) is globally unique and monotonically increasing.
type RaftOrderedLog struct {
	raft      *raft.Raft
	transport *raft.NetworkTransport

	// committed receives every transaction as soon as the FSM processes it.
	// The buffer absorbs bursts so the FSM's Apply() is never blocked by a
	// slow consumer.
	committed chan EncryptedTransaction
	done      chan struct{}
	closeOnce sync.Once
}

// Compile-time assertion: RaftOrderedLog must satisfy OrderedLog.
var _ OrderedLog = (*RaftOrderedLog)(nil)

// NewRaftOrderedLog starts a Raft node with the given configuration and returns
// a RaftOrderedLog once the node is ready to accept proposals.
//
// Storage layout under cfg.DataDir:
//
//	raft-log.bolt    — BoltDB log store (replication entries)
//	raft-stable.bolt — BoltDB stable store (term / vote metadata)
//	snapshots/       — periodic FSM snapshots (compacts the log)
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
	// Default commit buffer — large enough to smooth over short bursts.
	if cfg.CommitBuffer <= 0 {
		cfg.CommitBuffer = 1024
	}

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve raft bind address: %w", err)
	}

	// TCP transport is the standard network layer for Raft inter-node RPC.
	// We discard transport logs (io.Discard) to keep stdout clean.
	transport, err := raft.NewTCPTransport(cfg.BindAddress, addr, 3, 10*time.Second, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("create raft transport: %w", err)
	}

	committed := make(chan EncryptedTransaction, cfg.CommitBuffer)
	done := make(chan struct{})

	// The FSM (finite state machine) is called by the Raft library on every node
	// in the cluster whenever a log entry is committed. Our FSM simply deserialises
	// the proposal and forwards the resulting transaction to the committed channel.
	fsm := &raftFSM{
		committed: committed,
		done:      done,
	}

	raftConfig := raft.DefaultConfig()
	raftConfig.LocalID = raft.ServerID(cfg.NodeID)

	// Create the data directory before opening the bolt stores.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft data dir: %w", err)
	}

	logStorePath := filepath.Join(cfg.DataDir, "raft-log.bolt")
	stableStorePath := filepath.Join(cfg.DataDir, "raft-stable.bolt")
	snapshotDir := filepath.Join(cfg.DataDir, "snapshots")

	// LogStore holds raw Raft log entries (the replicated proposals).
	logStore, err := raftboltdb.NewBoltStore(logStorePath)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft log store: %w", err)
	}

	// StableStore holds durable metadata: current term and last vote.
	// Persisting these prevents a split-brain if the node restarts mid-election.
	stableStore, err := raftboltdb.NewBoltStore(stableStorePath)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("create raft stable store: %w", err)
	}

	// SnapshotStore keeps the last 2 FSM snapshots on disk to bound log growth.
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

	// Bootstrap: write the initial cluster configuration into the log store.
	// This is only done on the very first node that creates the cluster.
	// Subsequent nodes join via AddVoter() after the cluster is running.
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

		// Always include this node itself in the initial voter set.
		if !seenSelf {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(cfg.NodeID),
				Address: localAddr,
			})
		}

		// Edge case: no peers configured — single-node cluster.
		if len(servers) == 0 {
			servers = append(servers, raft.Server{
				ID:      raft.ServerID(cfg.NodeID),
				Address: localAddr,
			})
		}

		bootstrap := node.BootstrapCluster(raft.Configuration{Servers: servers})
		// ErrCantBootstrap means the log already has a configuration entry —
		// the cluster was bootstrapped previously. This is safe to ignore on restart.
		if err := bootstrap.Error(); err != nil && !errors.Is(err, raft.ErrCantBootstrap) {
			orderedLog.Close()
			return nil, fmt.Errorf("bootstrap raft cluster: %w", err)
		}
	}

	return orderedLog, nil
}

// SubmitWithReceipt proposes a transaction to the Raft cluster and blocks until
// the proposal is committed by a quorum of nodes (or the context expires).
//
// The returned EncryptedTransaction contains the Raft log index as its ID,
// which is the cluster-wide, totally ordered sequence number for this transaction.
//
// Only the Raft leader can process Apply() calls. Followers must proxy to the
// leader before calling this method.
func (r *RaftOrderedLog) SubmitWithReceipt(ctx context.Context, ciphertext []byte) (EncryptedTransaction, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedTransaction{}, err
	}

	// Deep copy: Raft serialises the payload asynchronously, so we must not
	// keep a reference to the caller's slice.
	payload := make([]byte, len(ciphertext))
	copy(payload, ciphertext)

	proposalBytes, err := json.Marshal(raftProposal{
		// Record submission time before the Apply() call so the timestamp
		// reflects when the client submitted, not when Raft committed.
		SubmittedAtUnixNano: time.Now().UnixNano(),
		Ciphertext:          payload,
	})
	if err != nil {
		return EncryptedTransaction{}, fmt.Errorf("marshal raft proposal: %w", err)
	}

	// Respect the caller's deadline: if they gave us a context deadline,
	// use it as the Raft apply timeout rather than the default.
	timeout := defaultRaftApplyTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return EncryptedTransaction{}, ctx.Err()
		}
	}

	// Apply() blocks until the entry is committed on a quorum. On the leader
	// it returns immediately once the commit index advances.
	future := r.raft.Apply(proposalBytes, timeout)
	if err := future.Error(); err != nil {
		return EncryptedTransaction{}, fmt.Errorf("raft apply failed: %w", err)
	}

	// The FSM's Apply() returns the EncryptedTransaction it constructed.
	// Type-asserting here validates that the FSM returned what we expected.
	tx, ok := future.Response().(EncryptedTransaction)
	if !ok {
		return EncryptedTransaction{}, errors.New("raft apply returned unexpected response type")
	}
	return tx, nil
}

// IsLeader returns true if this node is currently the Raft leader.
func (r *RaftOrderedLog) IsLeader() bool {
	return r.raft.State() == raft.Leader
}

// LeaderAddress returns the Raft transport address of the current leader,
// or an empty string if the cluster has no leader (e.g. during an election).
func (r *RaftOrderedLog) LeaderAddress() string {
	addr, _ := r.raft.LeaderWithID()
	return string(addr)
}

// AddVoter adds a new node to the cluster's voter set.
// Must only be called on the current leader.
func (r *RaftOrderedLog) AddVoter(id, address string) error {
	future := r.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, 0)
	return future.Error()
}

// StepDown voluntarily transfers leadership to another node. Used by the
// leader-lease mechanism to enable round-robin leadership rotation.
// No-ops if this node is already a follower.
func (r *RaftOrderedLog) StepDown() error {
	if !r.IsLeader() {
		return nil
	}
	fmt.Println("Leader lease expired, stepping down...")
	return r.raft.LeadershipTransfer().Error()
}

// DrainWait blocks until exactly batchSize committed transactions are available
// on the committed channel, then returns them in Raft log-index (FIFO) order.
//
// It returns early with whatever was collected if the context is cancelled or
// the log is closed.
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

// Close shuts down the Raft node and transport.
// Safe to call multiple times — only the first call does anything.
func (r *RaftOrderedLog) Close() {
	r.closeOnce.Do(func() {
		// Signal any goroutines blocking on the committed channel to stop.
		close(r.done)
		if r.raft != nil {
			_ = r.raft.Shutdown().Error()
		}
		if r.transport != nil {
			_ = r.transport.Close()
		}
	})
}

// raftFSM is the Finite State Machine given to the Raft library. It is called
// on every node in the cluster (leader and followers) whenever a log entry is
// committed. Its job is to turn the raw bytes of the log entry back into an
// EncryptedTransaction and forward it to the consumer goroutine.
type raftFSM struct {
	committed chan<- EncryptedTransaction // write-only; shared with RaftOrderedLog
	done      <-chan struct{}             // read-only; signals shutdown
}

// Apply is called by the Raft library on every committed log entry.
// It runs on all nodes simultaneously — after this returns, every node in the
// cluster has the same transaction in the same position.
//
// The Raft log index (log.Index) becomes the transaction's global sequence ID.
// This is safe because Raft guarantees that log indices are unique and strictly
// increasing across the entire cluster lifetime.
func (f *raftFSM) Apply(log *raft.Log) interface{} {
	var proposal raftProposal
	if err := json.Unmarshal(log.Data, &proposal); err != nil {
		return err
	}

	tx := EncryptedTransaction{
		ID:         log.Index,                                 // globally unique, monotonically increasing
		Ciphertext: append([]byte(nil), proposal.Ciphertext...), // defensive copy
		ArrivedAt:  time.Unix(0, proposal.SubmittedAtUnixNano),
	}

	// Send the transaction to the consumer goroutine. If the done channel is
	// closed first, we discard the transaction rather than blocking forever.
	select {
	case f.committed <- tx:
	case <-f.done:
	}

	// Return the transaction so SubmitWithReceipt can access it via future.Response().
	return tx
}

// Snapshot creates a point-in-time snapshot of the FSM state.
// Our FSM is effectively stateless (transactions flow through a channel and are
// consumed immediately), so we return a no-op snapshot. Raft will still compact
// the log based on the snapshot, but there is nothing to serialise.
func (f *raftFSM) Snapshot() (raft.FSMSnapshot, error) {
	return noopRaftSnapshot{}, nil
}

// Restore is called when a node restores state from a snapshot.
// Because we use a no-op snapshot there is nothing to restore.
func (f *raftFSM) Restore(io.ReadCloser) error {
	return nil
}

// noopRaftSnapshot satisfies the raft.FSMSnapshot interface with no-op implementations.
type noopRaftSnapshot struct{}

// Persist writes the snapshot to the sink. We have no state to persist, so
// we simply close the sink to signal completion.
func (noopRaftSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Close()
}

// Release is called after the snapshot is no longer needed. Nothing to do.
func (noopRaftSnapshot) Release() {}
