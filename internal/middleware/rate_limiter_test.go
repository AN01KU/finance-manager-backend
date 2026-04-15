package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_SkipsNonReleaseMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RateLimiter())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	// Should never be rate limited in test mode
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code, "request %d should pass in test mode", i)
	}
}

func TestRateLimiter_SweepRemovesExpiredEntries(t *testing.T) {
	rl := &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    5,
		window:   100 * time.Millisecond,
	}

	past := time.Now().Add(-200 * time.Millisecond)
	recent := time.Now()

	rl.requests["expired-ip"] = []time.Time{past, past}
	rl.requests["active-ip"] = []time.Time{recent}
	rl.requests["mixed-ip"] = []time.Time{past, recent}

	rl.sweep()

	assert.Nil(t, rl.requests["expired-ip"], "fully expired IP should be removed")
	assert.Len(t, rl.requests["active-ip"], 1, "active IP should remain")
	assert.Len(t, rl.requests["mixed-ip"], 1, "mixed IP should keep only recent entry")
	assert.Equal(t, recent, rl.requests["mixed-ip"][0])
}

func TestRateLimiter_SweepEmptyMap(t *testing.T) {
	rl := &rateLimiter{
		requests: make(map[string][]time.Time),
		limit:    5,
		window:   time.Second,
	}

	rl.sweep()
	assert.Empty(t, rl.requests, "sweep on empty map should not panic")
}
