package isureal

import (
	"container/list"
	"crypto/sha256"
	"strings"
	"sync"
)

// Keys include all inputs, so keys or IDs reused after initialization cannot
// produce a stale signature. Values are copied; callers cannot mutate entries.
type signatureCacheKey struct {
	Key     [sha256.Size]byte
	ImageID string
	Exp     int64
}

type signatureCacheEntry struct {
	key   signatureCacheKey
	value [sha256.Size]byte
}

type signatureCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[signatureCacheKey]*list.Element
	recent   list.List
}

var imageSignatures = newSignatureCache(65536)

func newSignatureCache(capacity int) *signatureCache {
	return &signatureCache{capacity: capacity, entries: make(map[signatureCacheKey]*list.Element, capacity)}
}

func (c *signatureCache) get(key signatureCacheKey) ([sha256.Size]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.recent.MoveToFront(e)
		return e.Value.(signatureCacheEntry).value, true
	}
	return [sha256.Size]byte{}, false
}

func (c *signatureCache) put(key signatureCacheKey, value [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		c.recent.MoveToFront(e)
		return
	}
	if c.capacity <= 0 {
		return
	}
	// Avoid retaining an entire request URL through a substring.
	key.ImageID = strings.Clone(key.ImageID)
	if len(c.entries) == c.capacity {
		oldest := c.recent.Back()
		delete(c.entries, oldest.Value.(signatureCacheEntry).key)
		oldest.Value = signatureCacheEntry{key: key, value: value}
		c.recent.MoveToFront(oldest)
		c.entries[key] = oldest
		return
	}
	c.entries[key] = c.recent.PushFront(signatureCacheEntry{key: key, value: value})
}
