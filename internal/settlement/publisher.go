package settlement

import "context"

// CommitmentPublisher publishes sequencer reception commitments to an external sink.
type CommitmentPublisher interface {
	PublishCommitment(ctx context.Context, commitmentHex string) error
}

type NoopPublisher struct{}

func (NoopPublisher) PublishCommitment(_ context.Context, _ string) error {
	return nil
}
