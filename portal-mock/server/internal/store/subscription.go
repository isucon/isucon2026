package store

import (
	"context"
	"sync"
)

// Subscription delivers an initial value and then the latest published value.
// Its buffer is one item, so slow consumers coalesce intermediate updates.
type Subscription[T any] struct {
	C      <-chan T
	cancel func()
}

func (s Subscription[T]) Cancel() { s.cancel() }

type hub[T any] struct {
	mu          sync.Mutex
	current     T
	subscribers map[uint64]chan T
	next        uint64
}

func newHub[T any](initial T) *hub[T] {
	return &hub[T]{current: initial, subscribers: make(map[uint64]chan T)}
}

func (h *hub[T]) subscribe(ctx context.Context) Subscription[T] {
	h.mu.Lock()
	h.next++
	id := h.next
	ch := make(chan T, 1)
	ch <- h.current
	h.subscribers[id] = ch
	h.mu.Unlock()

	var once sync.Once
	done := make(chan struct{})
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, id)
			h.mu.Unlock()
			close(done)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	}()
	return Subscription[T]{C: ch, cancel: cancel}
}

func (h *hub[T]) publish(value T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current = value
	for _, ch := range h.subscribers {
		select {
		case ch <- value:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- value
		}
	}
}
