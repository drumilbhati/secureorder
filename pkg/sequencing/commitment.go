package sequencing

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

type OrderMetadata struct {
	OrderID   string
	User      string
	Token     string
	Amount    float64
	Price     float64
	Timestamp int64
	Nonce     string
}

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

type ReceptionProof struct {
	SequenceID  uint64
	ArrivedUnix int64
	Commitment  string
}

// GenerateReceptionCommitment creates a proof hash as soon as the encrypted
// payload is accepted by the server queue.
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
