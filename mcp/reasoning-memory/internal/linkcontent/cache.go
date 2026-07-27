package linkcontent

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type cacheEntry struct {
	source    Source
	expiresAt time.Time
}

type Cache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	order      []string
	ttl        time.Duration
	maxEntries int
}

func NewCache(ttlMinutes, maxEntries int) *Cache {
	if ttlMinutes < 0 {
		ttlMinutes = 0
	}
	if maxEntries <= 0 {
		maxEntries = 256
	}
	return &Cache{entries: make(map[string]cacheEntry), ttl: time.Duration(ttlMinutes) * time.Minute, maxEntries: maxEntries}
}

func (c *Cache) Get(key string) (Source, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return Source{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		c.removeOrder(key)
		return Source{}, false
	}
	return cloneSource(entry.source), true
}

func (c *Cache) Put(key string, source Source) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = cacheEntry{source: cloneSource(source), expiresAt: time.Now().Add(c.ttl)}
	for len(c.entries) > c.maxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *Cache) Key(raw, contentHash string) string {
	normalized, _ := NormalizeURL(raw)
	h := sha256.Sum256([]byte(normalized + "\x00" + contentHash))
	return hex.EncodeToString(h[:])
}

func (c *Cache) removeOrder(key string) {
	for i, existing := range c.order {
		if existing == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func cloneSource(source Source) Source {
	source.Instructions = append([]string(nil), source.Instructions...)
	source.AcceptanceCriteria = append([]string(nil), source.AcceptanceCriteria...)
	source.Constraints = append([]string(nil), source.Constraints...)
	return source
}
