package app

import (
	"sync"
	"time"
)

// algoMeta is a snapshot of the algorithm metadata needed for invocation orchestration.
type algoMeta struct {
	name     string
	stateful bool
}

type metaEntry struct {
	meta    algoMeta
	expires time.Time
}

// metaCache is a TTL cache of algorithm metadata, preventing every concurrent call from hitting
// the database. Caching is disabled when ttl<=0.
type metaCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]metaEntry
}

func newMetaCache(ttl time.Duration) *metaCache {
	return &metaCache{ttl: ttl, m: map[string]metaEntry{}}
}

func (c *metaCache) get(code string) (algoMeta, bool) {
	if c.ttl <= 0 {
		return algoMeta{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[code]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.m, code)
		}
		return algoMeta{}, false
	}
	return e.meta, true
}

func (c *metaCache) set(code string, meta algoMeta) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.m[code] = metaEntry{meta: meta, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// invalidate drops a cached entry so the next call reloads fresh metadata.
func (c *metaCache) invalidate(code string) {
	c.mu.Lock()
	delete(c.m, code)
	c.mu.Unlock()
}
