package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/user"
)

func JWTAuth(jwtSecret string, database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authorization header required"})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "bearer token required"})
			c.Abort()
			return
		}

		token, err := jwt.ParseWithClaims(tokenString, &user.Claims{}, func(token *jwt.Token) (interface{}, error) {
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(*user.Claims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
			c.Abort()
			return
		}

		// Verify the user still exists AND that this JWT was issued after the
		// user's last token-invalidation event (logout / password change /
		// email change). Without this check, a JWT remains valid for its
		// full 24h TTL even after the user logs out — an attacker with a
		// leaked token can still read /me, /transactions, etc.
		if database != nil {
			var tokensInvalidatedAfter *time.Time
			err := database.Pool.QueryRow(c.Request.Context(),
				"SELECT tokens_invalidated_after FROM users WHERE id = $1", claims.UserID,
			).Scan(&tokensInvalidatedAfter)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "user no longer exists"})
				c.Abort()
				return
			}
			if tokensInvalidatedAfter != nil {
				if claims.IssuedAt == nil || !claims.IssuedAt.After(*tokensInvalidatedAfter) {
					c.JSON(http.StatusUnauthorized, gin.H{"error": "token revoked"})
					c.Abort()
					return
				}
			}
		}

		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Next()
	}
}

func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}
	id, ok := userID.(uuid.UUID)
	return id, ok
}

// RequireUserID extracts the authenticated user ID from the context.
// If missing, it responds with 401 and returns uuid.Nil, false.
func RequireUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.Nil, false
	}
	return userID, true
}
