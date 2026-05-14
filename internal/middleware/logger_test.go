package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
)

func TestRequestID_GeneratesUUID(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var capturedRID string
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get(applog.KeyRequestID)
		capturedRID, _ = v.(string)
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	_, err := uuid.Parse(capturedRID)
	require.NoError(t, err, "request_id stored in context must be a valid UUID")
}

func TestRequestID_EchosInResponseHeader(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	rid := w.Header().Get("X-Request-ID")
	_, err := uuid.Parse(rid)
	require.NoError(t, err, "X-Request-ID response header must be a valid UUID")
}

func TestRequestID_HonorsClientSuppliedUUID(t *testing.T) {
	supplied := uuid.NewString()

	r := gin.New()
	r.Use(RequestID())
	var capturedRID string
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get(applog.KeyRequestID)
		capturedRID, _ = v.(string)
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", supplied)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, supplied, capturedRID, "client-supplied UUID must be reused")
	assert.Equal(t, supplied, w.Header().Get("X-Request-ID"), "client-supplied UUID must be echoed in response header")
}

func TestRequestID_RejectsInvalidClientHeader(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var capturedRID string
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get(applog.KeyRequestID)
		capturedRID, _ = v.(string)
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "not-a-uuid")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should generate a fresh UUID instead of using the invalid client value.
	_, err := uuid.Parse(capturedRID)
	require.NoError(t, err, "invalid client X-Request-ID must be replaced with a fresh UUID")
	assert.NotEqual(t, "not-a-uuid", capturedRID)
}

func TestRequestID_InjectsLoggerWithRequestID(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var loggerSet bool
	r.GET("/test", func(c *gin.Context) {
		logger := applog.From(c)
		loggerSet = logger != nil
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.True(t, loggerSet, "per-request logger must be injected into gin context")
}

func TestRequestLogger_Returns200(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.Use(RequestLogger())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
}
