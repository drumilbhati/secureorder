// Package settlement publishes sequencer batch commitments to an external sink.
//
// The primary sink is an Ethereum-compatible smart contract (OrderVerifier)
// that records the commitment hash on-chain, providing an immutable,
// publicly verifiable proof of the transaction ordering produced by the sequencer.
//
// When EVM environment variables are absent (e.g. in local development) the
// package falls back to NoopPublisher, which accepts commitments silently
// without making any network calls.
package settlement

import "context"

// CommitmentPublisher is the interface satisfied by every settlement backend.
// Callers pass the hex-encoded SHA-256 batch commitment produced by
// sequencing.GenerateBatchCommitment.
type CommitmentPublisher interface {
	// PublishCommitment submits the commitment to the external sink.
	// Implementations should respect ctx for timeout/cancellation.
	// Returns nil on success, an error if the commitment could not be recorded.
	PublishCommitment(ctx context.Context, commitmentHex string) error
}

// NoopPublisher is a no-op implementation of CommitmentPublisher.
// It is used when the EVM environment variables are not set, allowing the
// sequencer to run in environments without an Ethereum node.
type NoopPublisher struct{}

// PublishCommitment does nothing and always succeeds.
func (NoopPublisher) PublishCommitment(_ context.Context, _ string) error {
	return nil
}
