package model

// Readiness is a bitmask of independent readiness levels.
// BM25 can be ready before embeddings, etc.
type Readiness uint8

const (
	ReadinessStored   Readiness = 1 << 0
	ReadinessEmbedded Readiness = 1 << 1
	ReadinessIndexed  Readiness = 1 << 2
	ReadinessArchived Readiness = 1 << 3
)

// Has returns true if all flags in the mask are set.
func (r Readiness) Has(mask Readiness) bool {
	return r&mask == mask
}

// Add sets the given flags.
func (r *Readiness) Add(mask Readiness) {
	*r |= mask
}

// Remove clears the given flags.
func (r *Readiness) Remove(mask Readiness) {
	*r &^= mask
}

const (
	ReadinessFull = ReadinessStored | ReadinessEmbedded | ReadinessIndexed
)
