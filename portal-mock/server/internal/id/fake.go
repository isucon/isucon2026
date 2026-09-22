package id

import (
	"fmt"
	"sync"
)

// Fake returns deterministic unique identifiers with the supplied prefix.
type Fake struct {
	mu     sync.Mutex
	prefix string
	next   uint64
}

func NewFake(prefix string) *Fake { return &Fake{prefix: prefix} }

func (f *Fake) New() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	return fmt.Sprintf("%s-%d", f.prefix, f.next)
}
