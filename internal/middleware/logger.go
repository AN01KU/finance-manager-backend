package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
)

// requestIDHeader is the HTTP header used to propagate the per-request
// correlation ID. Clients may set it to thread their own ID through; if
// missing or invalid we generate a fresh UUID v4.
const requestIDHeader = "X-Request-ID"

// RequestID generates a UUID per request, stores it in the gin context as
// "request_id" and on the per-request *slog.Logger, and echoes it back via
// the X-Request-ID response header so clients can correlate logs.
//
// Must be installed before RequestLogger and JWTAuth so downstream layers
// can pick up the logger via applog.From(c).
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(requestIDHeader)
		if _, err := uuid.Parse(rid); err != nil {
			rid = uuid.NewString()
		}
		c.Set(applog.KeyRequestID, rid)
		c.Writer.Header().Set(requestIDHeader, rid)
		applog.With(c, slog.Default().With(applog.KeyRequestID, rid))
		c.Next()
	}
}

// RequestLogger logs HTTP requests with timing information using slog.
// The per-request logger (set up by RequestID and enriched by JWTAuth)
// already carries request_id and user_id, so they are emitted automatically.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		duration := time.Since(start)

		applog.From(c).Info("http request",
			"method", method,
			"path", path,
			"proto", c.Request.Proto,
			"status", c.Writer.Status(),
			applog.KeyDurationMS, duration.Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}
