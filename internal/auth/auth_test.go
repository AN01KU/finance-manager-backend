package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/testutil"
	"github.com/yanonymousV2/finance-manager-backend/internal/user"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database := testutil.SetupDB(t)
	testutil.TruncateUsers(t, database)
	return database
}

func TestSignup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupTestDB(t)
	defer testDB.Close()

	service := &AuthService{
		DB:        testDB,
		JWTSecret: "test-secret",
	}

	tests := []struct {
		name           string
		requestBody    SignupRequest
		expectedStatus int
		expectError    bool
	}{
		{
			name: "valid signup",
			requestBody: SignupRequest{
				Email:    "test@example.com",
				Username: "testuser",
				Password: "password123",
			},
			expectedStatus: 201,
			expectError:    false,
		},
		{
			name: "duplicate email",
			requestBody: SignupRequest{
				Email:    "test@example.com",
				Username: "testuser2",
				Password: "password123",
			},
			expectedStatus: 400,
			expectError:    true,
		},
		{
			name: "invalid email",
			requestBody: SignupRequest{
				Email:    "invalid-email",
				Username: "testuser3",
				Password: "password123",
			},
			expectedStatus: 400,
			expectError:    true,
		},
		{
			name: "password too short",
			requestBody: SignupRequest{
				Email:    "test2@example.com",
				Username: "testuser4",
				Password: "123",
			},
			expectedStatus: 400,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			body, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("POST", "/auth/signup", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			Signup(c, service)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.Contains(t, response, "error")
			} else {
				assert.Contains(t, response, "token")
				assert.Contains(t, response, "user")
			}
		})
	}
}

func TestLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupTestDB(t)
	defer testDB.Close()

	service := &AuthService{
		DB:        testDB,
		JWTSecret: "test-secret",
	}

	// First create a user
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	userID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		userID, "login@example.com", "loginuser", string(hashedPassword))
	require.NoError(t, err)

	tests := []struct {
		name           string
		requestBody    LoginRequest
		expectedStatus int
		expectError    bool
	}{
		{
			name: "valid login",
			requestBody: LoginRequest{
				Email:    "login@example.com",
				Password: "password123",
			},
			expectedStatus: 200,
			expectError:    false,
		},
		{
			name: "invalid email",
			requestBody: LoginRequest{
				Email:    "nonexistent@example.com",
				Password: "password123",
			},
			expectedStatus: 401,
			expectError:    true,
		},
		{
			name: "wrong password",
			requestBody: LoginRequest{
				Email:    "login@example.com",
				Password: "wrongpassword",
			},
			expectedStatus: 401,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			body, _ := json.Marshal(tt.requestBody)
			c.Request = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			Login(c, service)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.expectError {
				assert.Contains(t, response, "error")
			} else {
				assert.Contains(t, response, "token")
				assert.Contains(t, response, "user")
			}
		})
	}
}

// TestLogin_FailedAttemptsResetAfterLockoutExpires verifies that a fresh
// login attempt arriving after the lockout window has elapsed starts the
// failed-attempt counter from 0, rather than continuing from the count that
// caused the lockout. Without this reset a single wrong password after the
// window re-locks the account immediately at threshold.
func TestLogin_FailedAttemptsResetAfterLockoutExpires(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupTestDB(t)
	defer testDB.Close()

	service := &AuthService{
		DB:        testDB,
		JWTSecret: "test-secret",
	}

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	userID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		userID, "lockoutexpired@example.com", "lockoutuser", string(hashedPassword))
	require.NoError(t, err)

	// Simulate a user who hit the lockout threshold and has now waited it
	// out: failed_login_attempts at the cap, login_locked_until in the past.
	_, err = testDB.Pool.Exec(context.Background(),
		`UPDATE users
		 SET failed_login_attempts = $1,
		     login_locked_until = now() - INTERVAL '1 minute'
		 WHERE id = $2`,
		maxFailedLoginAttempts, userID)
	require.NoError(t, err)

	// Wrong password attempt arriving after the window expired.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(LoginRequest{
		Email:    "lockoutexpired@example.com",
		Password: "wrongpassword",
	})
	c.Request = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Login(c, service)

	// Expect a plain 401 — not a 429 — because the prior lockout has expired.
	assert.Equal(t, 401, w.Code)

	// And the counter must reflect a fresh attempt: 1, not maxFailedLoginAttempts+1.
	var attempts int
	var lockedUntil *time.Time
	err = testDB.Pool.QueryRow(context.Background(),
		`SELECT failed_login_attempts, login_locked_until FROM users WHERE id = $1`, userID).
		Scan(&attempts, &lockedUntil)
	require.NoError(t, err)
	assert.Equal(t, 1, attempts, "fresh attempt after lockout expiry should reset counter to 1")
	assert.Nil(t, lockedUntil, "lockout column should be cleared on the reset")
}

// TestLogin_BumpsTokenInvalidationButNewJWTSurvives verifies the security
// requirement that Login bumps users.tokens_invalidated_after to revoke any
// JWT that pre-dated this login (closes the stolen-device-after-login gap),
// while the JWT issued by this very Login call must still pass middleware
// checks. The 1-second backwards offset on the bump is what reconciles
// these two requirements.
func TestLogin_BumpsTokenInvalidationButNewJWTSurvives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupTestDB(t)
	defer testDB.Close()

	service := &AuthService{
		DB:        testDB,
		JWTSecret: "test-secret",
	}

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	userID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		userID, "loginbump@example.com", "bumpuser", string(hashedPassword))
	require.NoError(t, err)

	// Snapshot any pre-existing tokens_invalidated_after (should be NULL).
	var beforeBump *time.Time
	err = testDB.Pool.QueryRow(context.Background(),
		`SELECT tokens_invalidated_after FROM users WHERE id = $1`, userID).Scan(&beforeBump)
	require.NoError(t, err)
	require.Nil(t, beforeBump)

	// Call Login.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(LoginRequest{
		Email:    "loginbump@example.com",
		Password: "password123",
	})
	c.Request = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Login(c, service)
	require.Equal(t, 200, w.Code)

	// The bump must have happened: tokens_invalidated_after is now non-null.
	var afterBump *time.Time
	err = testDB.Pool.QueryRow(context.Background(),
		`SELECT tokens_invalidated_after FROM users WHERE id = $1`, userID).Scan(&afterBump)
	require.NoError(t, err)
	require.NotNil(t, afterBump, "Login must bump tokens_invalidated_after")

	// And the JWT just issued by this Login must still pass JWTAuth — i.e.
	// the bumped timestamp must be at least one second behind the JWT's
	// IssuedAt, otherwise the token would be revoked the moment it's used.
	var resp AuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	parsed, err := jwt.ParseWithClaims(resp.Token, &user.Claims{}, func(*jwt.Token) (interface{}, error) {
		return []byte(service.JWTSecret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	claims := parsed.Claims.(*user.Claims)
	require.NotNil(t, claims.IssuedAt)
	assert.True(t,
		claims.IssuedAt.After(*afterBump),
		"new JWT IssuedAt (%s) must be After bumped tokens_invalidated_after (%s)",
		claims.IssuedAt.Time.Format(time.RFC3339Nano),
		afterBump.Format(time.RFC3339Nano),
	)
}

// TestLogin_InvalidatesJWTCacheEntry verifies that Login (which bumps
// users.tokens_invalidated_after) also drops the in-process JWT cache entry
// for that user, so the next authenticated request reads the fresh row
// instead of an ~10s-stale cached value.
func TestLogin_InvalidatesJWTCacheEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupTestDB(t)
	defer testDB.Close()

	cache := middleware.NewJWTRevocationCache(10 * time.Second)
	service := &AuthService{
		DB:        testDB,
		JWTSecret: "test-secret",
		JWTCache:  cache,
	}

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	userID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		userID, "cacheinvalidate@example.com", "cacheuser", string(hashedPassword))
	require.NoError(t, err)

	// Pre-warm the cache with a stale value.
	stale := time.Now().Add(-1 * time.Hour)
	cache.Set(userID, &stale)
	if _, ok := cache.Get(userID); !ok {
		t.Fatal("cache pre-warm failed")
	}

	// Login must invalidate the cache entry.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(LoginRequest{
		Email:    "cacheinvalidate@example.com",
		Password: "password123",
	})
	c.Request = httptest.NewRequest("POST", "/auth/login", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Login(c, service)
	require.Equal(t, 200, w.Code)

	_, ok := cache.Get(userID)
	assert.False(t, ok, "Login must drop the JWT cache entry so the next request reads the fresh tokens_invalidated_after")
}
