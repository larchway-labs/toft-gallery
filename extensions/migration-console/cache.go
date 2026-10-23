package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// The platform meters /api/v1/app/* per app token — 60 calls a minute, a burst
// of ten — and the console has **one** token for everybody using it. Every page
// asks who the viewer is and what the app may do, and a request page polls its
// progress every ten seconds, so uncached, a handful of people watching a
// release spend the whole budget and the next page is told "429". These caches
// are what keep the console inside it.
//
// Only answers that are true for a while are kept. Capabilities change when an
// administrator attaches something, and a minute's lag there is harmless. A
// viewer is keyed by the act-for blob the proxy put on the request, which is
// fixed for the life of one session (user, app, token version, session expiry):
// the same blob always names the same person, so the only thing a cached answer
// can miss is a revocation, by at most viewerTTL.
const (
	viewerTTL  = 30 * time.Second
	appWideTTL = time.Minute
)

type ttlCache[V any] struct {
	mu   sync.Mutex
	ttl  time.Duration
	vals map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	v  V
	at time.Time
}

func newTTLCache[V any](ttl time.Duration) *ttlCache[V] {
	return &ttlCache[V]{ttl: ttl, vals: map[string]ttlEntry[V]{}}
}

func (c *ttlCache[V]) get(key string, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.vals[key]
	if !ok || now.Sub(e.at) > c.ttl {
		var zero V
		return zero, false
	}
	return e.v, true
}

func (c *ttlCache[V]) put(key string, v V, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bounded by sweeping on write: a session's blob is the key, so the map
	// would otherwise grow by one entry per person per session for ever.
	if len(c.vals) > 512 {
		for k, e := range c.vals {
			if now.Sub(e.at) > c.ttl {
				delete(c.vals, k)
			}
		}
	}
	c.vals[key] = ttlEntry[V]{v: v, at: now}
}

func blobKey(blob string) string {
	sum := sha256.Sum256([]byte(blob))
	return hex.EncodeToString(sum[:])
}
