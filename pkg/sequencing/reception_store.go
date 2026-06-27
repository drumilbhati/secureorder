package sequencing

import "sync"

// ReceptionStore keeps an ordered, in-memory log of proof-of-reception
// commitments. Each entry records the sequence ID, arrival timestamp, and
// ciphertext hash for a single transaction as it was accepted at ingress —
// before decryption.
//
// The store is append-only and grows without bound during a sequencer session.
// It is used to:
//   - Answer "how many transactions has this node sequenced?" queries.
//   - Provide the last commitment for health-check or monitoring purposes.
//
// Thread-safe: Add uses a write lock; Count and Last use read locks.
type ReceptionStore struct {
	mu     sync.RWMutex
	proofs []ReceptionProof
}

// NewReceptionStore creates an empty store pre-allocated for 1024 proofs.
func NewReceptionStore() *ReceptionStore {
	return &ReceptionStore{proofs: make([]ReceptionProof, 0, 1024)}
}

// Add appends a reception proof to the store. The caller is responsible for
// ensuring proofs are added in sequence-ID order (which is natural when called
// from the gRPC SubmitTx handler, as IDs are assigned monotonically).
func (s *ReceptionStore) Add(p ReceptionProof) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs = append(s.proofs, p)
}

// Count returns the total number of proofs stored.
func (s *ReceptionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.proofs)
}

// Last returns the most recently added proof and true, or the zero value and
// false if no proofs have been stored yet.
func (s *ReceptionStore) Last() (ReceptionProof, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.proofs) == 0 {
		return ReceptionProof{}, false
	}
	return s.proofs[len(s.proofs)-1], true
}
