package sequencing

import "sync"

// ReceptionStore keeps proof-of-reception commitments in sequence order.
type ReceptionStore struct {
	mu     sync.RWMutex
	proofs []ReceptionProof
}

func NewReceptionStore() *ReceptionStore {
	return &ReceptionStore{proofs: make([]ReceptionProof, 0, 1024)}
}

func (s *ReceptionStore) Add(p ReceptionProof) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs = append(s.proofs, p)
}

func (s *ReceptionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.proofs)
}

func (s *ReceptionStore) Last() (ReceptionProof, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.proofs) == 0 {
		return ReceptionProof{}, false
	}
	return s.proofs[len(s.proofs)-1], true
}
