package middleware

import (
	"net/http/httptest"
	"testing"

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
