package domain

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type IDGenerator interface{ New() uuid.UUID }

type RandomIDGenerator struct{}

func (RandomIDGenerator) New() uuid.UUID { return uuid.New() }

type DeterministicIDGenerator struct {
	mu        sync.Mutex
	namespace uuid.UUID
	next      uint64
}

func NewDeterministicIDGenerator(namespace uuid.UUID) *DeterministicIDGenerator {
	return &DeterministicIDGenerator{namespace: namespace}
}

func (g *DeterministicIDGenerator) New() uuid.UUID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return uuid.NewSHA1(g.namespace, []byte(fmt.Sprintf("%020d", g.next)))
}
