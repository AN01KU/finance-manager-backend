package middleware

import (
	"github.com/gin-gonic/gin"
)

// CORS middleware to handle cross-origin requests.
// allowedOrigin controls which origin is permitted; use "*" for development only.
func CORS(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := allowedOrigin
		if origin == "" {
			origin = "*"
		}

		c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		if origin != "*" {
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Sync-Session-ID, X-Sync-Version")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
