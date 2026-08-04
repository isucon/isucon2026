package id

import "github.com/google/uuid"

// Generator creates opaque, non-ordered identifiers.
type Generator interface {
	New() string
}

type UUID struct{}

func (UUID) New() string { return uuid.NewString() }
