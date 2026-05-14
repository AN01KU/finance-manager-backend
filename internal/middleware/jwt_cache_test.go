package middleware

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestJWTRevocationCache_GetMissReturnsNotOk(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Second)
	_, ok := c.Get(uuid.New())
	assert.False(t, ok, "empty cache must report a miss")
}

func TestJWTRevocationCache_SetThenGetReturnsValue(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Second)
	uid := uuid.New()
	stamp := time.Now().Add(-1 * time.Second)
	c.Set(uid, &stamp)
	got, ok := c.Get(uid)
	assert.True(t, ok)
	if assert.NotNil(t, got) {
		assert.Equal(t, stamp.UnixNano(), got.UnixNano())
	}
}

func TestJWTRevocationCache_NilValueIsCacheable(t *testing.T) {
	// users.tokens_invalidated_after is NULL for users who have never
	// logged out / rotated credentials. Caching that nil result avoids
	// an SQL round-trip for every authenticated request from such users.
	c := NewJWTRevocationCache(10 * time.Second)
	uid := uuid.New()
	c.Set(uid, nil)
	got, ok := c.Get(uid)
	assert.True(t, ok, "nil-valued entries must still be reported as hits")
	assert.Nil(t, got)
}

func TestJWTRevocationCache_ExpiredEntryIsMiss(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Millisecond)
	uid := uuid.New()
	stamp := time.Now()
	c.Set(uid, &stamp)
	time.Sleep(20 * time.Millisecond)
	_, ok := c.Get(uid)
	assert.False(t, ok, "entry past its TTL must be treated as a miss")
}

func TestJWTRevocationCache_InvalidateRemovesEntry(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Second)
	uid := uuid.New()
	stamp := time.Now()
	c.Set(uid, &stamp)
	c.Invalidate(uid)
	_, ok := c.Get(uid)
	assert.False(t, ok, "invalidated entry must be a miss")
}

func TestJWTRevocationCache_InvalidateUnknownIsNoOp(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Second)
	c.Invalidate(uuid.New()) // no panic
}

func TestJWTRevocationCache_ConcurrentReadsAndWrites(t *testing.T) {
	c := NewJWTRevocationCache(10 * time.Second)
	uid := uuid.New()
	stamp := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.Set(uid, &stamp) }()
		go func() { defer wg.Done(); _, _ = c.Get(uid) }()
	}
	wg.Wait()
}
