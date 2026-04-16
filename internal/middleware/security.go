package middleware

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// limitedBody wraps a MaxBytesReader and aborts the Gin context with 413
// on the first read that exceeds the limit.
type limitedBody struct {
	io.ReadCloser
	c       *gin.Context
	aborted bool
}

func (l *limitedBody) Read(p []byte) (int, error) {
	n, err := l.ReadCloser.Read(p)
	if err != nil && !l.aborted {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			l.aborted = true
			l.c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
		}
	}
	return n, err
}

// BodyLimit wraps the request body with an http.MaxBytesReader to reject
// bodies larger than maxBytes with HTTP 413. Should be registered before any
// handler that reads the request body.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		limited := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Request.Body = &limitedBody{ReadCloser: limited, c: c}
		c.Next()
	}
}

// SecurityHeaders sets common security headers on every response.
// In release mode it also adds Strict-Transport-Security.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "same-origin")
		if gin.Mode() == gin.ReleaseMode {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
