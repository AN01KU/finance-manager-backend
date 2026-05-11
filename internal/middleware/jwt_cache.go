package middleware

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// JWTRevocationCache is a tiny per-user cache for users.tokens_invalidated_after,
// the column JWTAuth consults on every authenticated request to decide whether
// a token has been revoked by a logout / password change / email change / login.
//
// At v1 the lookup is a hot per-request SELECT against the users table. The
// cache lets us skip that SELECT for ~ttl after the first hit and is wiped
// inline by every code path that bumps the column, so freshness is preserved
// without polling.
//
// nil values are first-class: a NULL column means "never invalidated" and is
// the common case, so caching nil avoids the SQL hop for users who have
// never logged out.
type JWTRevocationCache struct {
	mu      sync.RWMutex
	entries map[uuid.UUID]jwtCacheEntry
	ttl     time.Duration
}

type jwtCacheEntry struct {
	tokensInvalidatedAfter *time.Time
	expiresAt              time.Time
}

// NewJWTRevocationCache returns a cache whose entries live for ttl.
func NewJWTRevocationCache(ttl time.Duration) *JWTRevocationCache {
	return &JWTRevocationCache{
		entries: make(map[uuid.UUID]jwtCacheEntry),
		ttl:     ttl,
	}
}

// Get returns the cached value (which may be nil) and ok=true on a fresh hit.
// Misses and expired entries return ok=false.
func (c *JWTRevocationCache) Get(userID uuid.UUID) (*time.Time, bool) {
	c.mu.RLock()
	e, ok := c.entries[userID]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		// Expired — drop opportunistically so the map doesn't grow unbounded
		// for one-shot users; safe under the write lock.
		c.mu.Lock()
		delete(c.entries, userID)
		c.mu.Unlock()
		return nil, false
	}
	return e.tokensInvalidatedAfter, true
}

// Set caches the value (nil is allowed and meaningful) for ttl.
func (c *JWTRevocationCache) Set(userID uuid.UUID, value *time.Time) {
	c.mu.Lock()
	c.entries[userID] = jwtCacheEntry{
		tokensInvalidatedAfter: value,
		expiresAt:              time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// Invalidate drops the entry for userID. Call this from every code path that
// bumps users.tokens_invalidated_after (logout, password change, email change,
// login) so the next authenticated request reads the fresh row.
func (c *JWTRevocationCache) Invalidate(userID uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, userID)
	c.mu.Unlock()
}
