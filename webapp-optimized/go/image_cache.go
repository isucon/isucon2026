package isureal

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

const imageCacheBytes = 64 << 20
const imageCacheEntries = 4096

// Cached data is immutable. A get keeps its own reference even if another
// request replaces/evicts the entry while the database check is in progress.
type cachedImage struct {
	id      string
	version string
	data    []byte
	// Owner's signing key and media type as of checkedAt, when the image was
	// last confirmed to be live.
	secretKeyHash string
	mediaType     string
	checkedAt     time.Time
}

type imageCache struct {
	mu                       sync.Mutex
	budget, maxEntries, used int
	entries                  map[string]*list.Element
	recent                   list.List
}

func newImageCache(budget, maxEntries int) *imageCache {
	return &imageCache{budget: budget, maxEntries: maxEntries, entries: make(map[string]*list.Element)}
}

func (c *imageCache) get(id string) (cachedImage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[id]; ok {
		c.recent.MoveToFront(entry)
		return entry.Value.(cachedImage), true
	}
	return cachedImage{}, false
}

func (c *imageCache) put(id string, object imageObject, checkedAt time.Time) {
	version, data := object.Version, object.Data
	// Bound both retained payload bytes and per-entry overhead, including empty images.
	if len(data) > c.budget || c.maxEntries <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[id]; ok {
		c.used -= len(old.Value.(cachedImage).data)
		delete(c.entries, id)
		c.recent.Remove(old)
	}
	for c.used+len(data) > c.budget || len(c.entries) >= c.maxEntries {
		old := c.recent.Back()
		value := old.Value.(cachedImage)
		c.used -= len(value.data)
		delete(c.entries, value.id)
		c.recent.Remove(old)
	}
	value := cachedImage{id: strings.Clone(id), version: version, data: data,
		secretKeyHash: object.SecretKeyHash, mediaType: object.MediaType, checkedAt: checkedAt}
	c.entries[value.id] = c.recent.PushFront(value)
	c.used += len(data)
}
