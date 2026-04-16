package middleware

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBodyLimit(t *testing.T) {
	t.Run("small body is allowed", func(t *testing.T) {
		r := gin.New()
		r.Use(BodyLimit(1 << 20)) // 1 MiB
		r.POST("/test", func(c *gin.Context) { c.Status(200) })

		body := bytes.Repeat([]byte("a"), 512)
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)
	})

	t.Run("body exceeding limit is rejected with 413", func(t *testing.T) {
		r := gin.New()
		r.Use(BodyLimit(512)) // 512 bytes limit
		r.POST("/test", func(c *gin.Context) {
			// Force body read to trigger the limit
			buf := make([]byte, 1024)
			_, _ = c.Request.Body.Read(buf)
			c.Status(200)
		})

		body := bytes.Repeat([]byte("a"), 1024)
		req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, 413, w.Code)
	})
}

func TestSecurityHeaders(t *testing.T) {
	t.Run("non-release mode sets basic security headers", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(SecurityHeaders())
		r.GET("/test", func(c *gin.Context) { c.Status(200) })

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
		assert.Equal(t, "same-origin", w.Header().Get("Referrer-Policy"))
	})

	t.Run("release mode sets HSTS header", func(t *testing.T) {
		gin.SetMode(gin.ReleaseMode)
		defer gin.SetMode(gin.TestMode)

		r := gin.New()
		r.Use(SecurityHeaders())
		r.GET("/test", func(c *gin.Context) { c.Status(200) })

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)
		hsts := w.Header().Get("Strict-Transport-Security")
		assert.True(t, strings.Contains(hsts, "max-age="), "HSTS header should contain max-age")
	})
}
