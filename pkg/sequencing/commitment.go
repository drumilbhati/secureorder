package sequencing

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// OrderMetadata describes a plaintext DEX order before encryption.
// This struct is used only by GenerateCommitment (the legacy single-order
// commitment helper). In production, transactions arrive as ciphertexts and
// this struct is populated only after decryption.
type OrderMetadata struct {
	OrderID   string
	User      string
	Token     string
	Amount    float64
	Price     float64
	Timestamp int64
	Nonce     string // prevents two identical orders from producing the same commitment hash
}

// GenerateCommitment produces a deterministic SHA-256 hex digest of an order's
// metadata fields. The pipe-delimited format ensures that concatenating adjacent
// fields cannot produce the same string as a different field assignment
// (e.g. "AB|C" ≠ "A|BC").
//
// This is used for single-order commitments in the legacy flow. For batch
// commitments use GenerateBatchCommitment.
func GenerateCommitment(order OrderMetadata) string {
	data := fmt.Sprintf(
		"%s|%s|%s|%f|%f|%d|%s",
		order.OrderID,
		order.User,
		order.Token,
		order.Amount,
		order.Price,
		order.Timestamp,
		order.Nonce,
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// ReceptionProof records the sequencer's acknowledgement that a specific
// encrypted transaction was received and given a sequence ID. It can be
// used to audit the sequencer's claimed FIFO ordering post-reveal.
type ReceptionProof struct {
	SequenceID  uint64 // Raft log index (or TxQueue counter) assigned at ingress
	ArrivedUnix int64  // nanosecond epoch timestamp recorded by the admission loop
	Commitment  string // hex-encoded SHA-256 of (SequenceID || ArrivedUnix || Ciphertext)
}

// GenerateReceptionCommitment creates a proof hash for a single transaction at
// ingress time — before the batch is assembled and before decryption.
//
// The hash covers:
//   - tx.ID       (8 bytes, little-endian) — the assigned sequence number
//   - tx.ArrivedAt (8 bytes, little-endian nanoseconds) — the admission timestamp
//   - tx.Ciphertext (variable length) — the opaque encrypted payload
//
// Using little-endian fixed-width encoding for the integers (rather than string
// formatting) avoids ambiguities where, for example, ID=12 and ArrivedAt=3
// could collide with ID=1 and ArrivedAt=23 under a naive string approach.
func GenerateReceptionCommitment(tx EncryptedTransaction) string {
	h := sha256.New()

	var id [8]byte
	binary.LittleEndian.PutUint64(id[:], tx.ID)
	_, _ = h.Write(id[:])

	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(tx.ArrivedAt.UnixNano()))
	_, _ = h.Write(ts[:])

	_, _ = h.Write(tx.Ciphertext)

	return hex.EncodeToString(h.Sum(nil))
}

// GenerateBatchCommitment creates a single root hash covering an entire ordered
// batch of transactions. This is the value published to the on-chain
// OrderVerifier contract as proof of sequencing.
//
// The hash is computed by feeding (ID, ArrivedAt, Ciphertext) for each
// transaction into a single SHA-256 hasher in strict FIFO order. This means:
//
//   - The same set of transactions in a different order produces a different hash
//     (because the ID and order of writes to the hasher differ).
//   - Any tampering with a single transaction's ciphertext or timestamp changes
//     the root hash, making the commitment unforgeable.
//
// The resulting hex string is published to the OrderVerifier.commitOrder()
// function on-chain.
func GenerateBatchCommitment(batch []EncryptedTransaction) string {
	h := sha256.New()
	for _, tx := range batch {
		var id [8]byte
		binary.LittleEndian.PutUint64(id[:], tx.ID)
		_, _ = h.Write(id[:])

		var ts [8]byte
		binary.LittleEndian.PutUint64(ts[:], uint64(tx.ArrivedAt.UnixNano()))
		_, _ = h.Write(ts[:])

		_, _ = h.Write(tx.Ciphertext)
	}
	return hex.EncodeToString(h.Sum(nil))
}
