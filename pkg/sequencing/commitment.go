package sequencing

import (
	"crypto/sha256"
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
