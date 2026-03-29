package middleware

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimiter struct {
	requests map[string][]time.Time
	mu       sync.Mutex
	limit    int
	window   time.Duration
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

var limiter = &rateLimiter{
	requests: make(map[string][]time.Time),
	limit:    getEnvInt("RATE_LIMIT", 10),
	window:   time.Duration(getEnvInt("RATE_WINDOW_SECONDS", 60)) * time.Second,
}

// RateLimiter middleware to prevent brute force attacks.
// Skipped in non-release mode (debug/test) for easier local development.
func RateLimiter() gin.HandlerFunc {
	return func(c *gin.Context) {
		if gin.Mode() != gin.ReleaseMode {
			c.Next()
			return
		}

		ip := c.ClientIP()

		limiter.mu.Lock()
		defer limiter.mu.Unlock()

		now := time.Now()

		// Get requests for this IP
		requests, exists := limiter.requests[ip]
		if !exists {
			limiter.requests[ip] = []time.Time{now}
			c.Next()
			return
		}

		// Filter out requests outside the time window
		var validRequests []time.Time
		for _, reqTime := range requests {
			if now.Sub(reqTime) < limiter.window {
				validRequests = append(validRequests, reqTime)
			}
		}

		// Check if limit exceeded
		if len(validRequests) >= limiter.limit {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded, please try again later",
			})
			c.Abort()
			return
		}

		// Add current request
		validRequests = append(validRequests, now)
		limiter.requests[ip] = validRequests

		// Cleanup old entries periodically (simple approach)
		if len(limiter.requests) > 1000 {
			for ip, reqs := range limiter.requests {
				if len(reqs) == 0 || now.Sub(reqs[len(reqs)-1]) > limiter.window {
					delete(limiter.requests, ip)
				}
			}
		}

		c.Next()
	}
}
