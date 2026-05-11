package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/yanonymousV2/finance-manager-backend/internal/user"
)

func makeToken(t *testing.T, secret string, userID uuid.UUID, email string, expiresAt time.Time) string {
	t.Helper()
	claims := user.Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestJWTAuth(t *testing.T) {
	secret := "test-secret-that-is-at-least-32-chars-long"
	userID := uuid.New()

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantUserID bool
	}{
		{
			name:       "missing header",
			authHeader: "",
			wantStatus: 401,
		},
		{
			name:       "no bearer prefix",
			authHeader: "Token abc123",
			wantStatus: 401,
		},
		{
			name:       "invalid token",
			authHeader: "Bearer invalid.token.here",
			wantStatus: 401,
		},
		{
			name:       "expired token",
			authHeader: "Bearer " + makeToken(t, secret, userID, "test@test.com", time.Now().Add(-1*time.Hour)),
			wantStatus: 401,
		},
		{
			name:       "wrong secret",
			authHeader: "Bearer " + makeToken(t, "wrong-secret-that-is-at-least-32-chars", userID, "test@test.com", time.Now().Add(1*time.Hour)),
			wantStatus: 401,
		},
		{
			name:       "valid token",
			authHeader: "Bearer " + makeToken(t, secret, userID, "test@test.com", time.Now().Add(1*time.Hour)),
			wantStatus: 200,
			wantUserID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(JWTAuth(secret, nil, nil))
			var gotUserID uuid.UUID
			r.GET("/test", func(c *gin.Context) {
				if id, ok := GetUserID(c); ok {
					gotUserID = id
				}
				c.Status(200)
			})

			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantUserID {
				assert.Equal(t, userID, gotUserID)
			}
		})
	}
}

func TestGetUserID(t *testing.T) {
	t.Run("missing context value", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		id, ok := GetUserID(c)
		assert.False(t, ok)
		assert.Equal(t, uuid.Nil, id)
	})

	t.Run("wrong type in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", "not-a-uuid")
		id, ok := GetUserID(c)
		assert.False(t, ok)
		assert.Equal(t, uuid.Nil, id)
	})

	t.Run("valid UUID in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		expected := uuid.New()
		c.Set("user_id", expected)
		id, ok := GetUserID(c)
		assert.True(t, ok)
		assert.Equal(t, expected, id)
	})
}
