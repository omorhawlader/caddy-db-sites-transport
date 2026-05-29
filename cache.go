package dbsites

import (
	"sync"
	"time"
)

const defaultCacheTTL = 30 * time.Minute

type cacheEntry struct {
	site     *publishedSite
	found    bool
	expires  time.Time
	httpCode int
}

type responseCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

func newResponseCache() *responseCache {
	return &responseCache{entries: make(map[string]*cacheEntry)}
}

func (c *responseCache) get(key string) (*publishedSite, bool, bool, int) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false, false, 0
	}
	if time.Now().After(entry.expires) {
		c.mu.Lock()
		if e, ok := c.entries[key]; ok && time.Now().After(e.expires) {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false, false, 0
	}
	return entry.site, entry.found, true, entry.httpCode
}

func (c *responseCache) set(key string, site *publishedSite, found bool, ttl time.Duration, httpCode int) {
	c.mu.Lock()
	c.entries[key] = &cacheEntry{
		site:     site,
		found:    found,
		expires:  time.Now().Add(ttl),
		httpCode: httpCode,
	}
	c.mu.Unlock()
}

func (c *responseCache) invalidateAll() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.entries)
	c.entries = make(map[string]*cacheEntry)
	return n
}

func resolveTTL(handlerTTL time.Duration) time.Duration {
	if handlerTTL > 0 {
		return handlerTTL
	}
	return defaultCacheTTL
}
