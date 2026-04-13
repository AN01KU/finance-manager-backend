package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/user"
)

var validate = validator.New()

type SignupRequest struct {
	Email      string `json:"email" validate:"required,email"`
	Username   string `json:"username" validate:"required,min=3"`
	Password   string `json:"password" validate:"required,min=6"`
	InviteCode string `json:"invite_code"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type AuthResponse struct {
	Token         string    `json:"token"`
	SyncSessionID uuid.UUID `json:"sync_session_id"`
	User          user.User `json:"user"`
}

type AuthService struct {
	DB         *db.DB
	JWTSecret  string
	InviteCode string
	OnSignup   func(ctx context.Context, userID uuid.UUID) // called after user creation
}

func Signup(c *gin.Context, service *AuthService) {
	database := service.DB
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Check invite code (configured via INVITE_CODE env var)
	if service.InviteCode != "" {
		if req.InviteCode != service.InviteCode {
			c.JSON(403, gin.H{"error": "invalid invite code"})
			return
		}
	}

	// Check if user exists
	var count int
	err := database.Pool.QueryRow(c.Request.Context(), "SELECT COUNT(*) FROM users WHERE email = $1", req.Email).Scan(&count)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if count > 0 {
		c.JSON(400, gin.H{"error": "user already exists"})
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to hash password"})
		return
	}

	// Insert user
	var u user.User
	var rawCreatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		"INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id, email, username, created_at",
		req.Email, req.Username, string(hash)).Scan(&u.ID, &u.Email, &u.Username, &rawCreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create user"})
		return
	}
	u.CreatedAt = helpers.FromTime(rawCreatedAt)

	// Run post-signup hook (e.g., seed predefined categories)
	if service.OnSignup != nil {
		service.OnSignup(c.Request.Context(), u.ID)
	}

	// Generate token
	token, err := generateToken(u.ID, u.Email, service.JWTSecret)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to generate token"})
		return
	}

	// Create sync session
	syncSessionID, err := createSyncSession(c.Request.Context(), database, u.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create sync session"})
		return
	}

	c.JSON(201, AuthResponse{Token: token, SyncSessionID: syncSessionID, User: u})
}

func GetMe(c *gin.Context, service *AuthService) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	userID, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var u user.User
	var rawCreatedAt time.Time
	err := service.DB.Pool.QueryRow(c.Request.Context(),
		"SELECT id, email, username, created_at FROM users WHERE id = $1", userID).Scan(
		&u.ID, &u.Email, &u.Username, &rawCreatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	u.CreatedAt = helpers.FromTime(rawCreatedAt)

	c.JSON(200, u)
}

func Login(c *gin.Context, service *AuthService) {
	database := service.DB
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Get user
	var u user.User
	var rawCreatedAt time.Time
	err := database.Pool.QueryRow(c.Request.Context(),
		"SELECT id, email, username, password_hash, created_at FROM users WHERE email = $1", req.Email).Scan(
		&u.ID, &u.Email, &u.Username, &u.PasswordHash, &rawCreatedAt)
	if err != nil {
		c.JSON(401, gin.H{"error": "invalid credentials"})
		return
	}
	u.CreatedAt = helpers.FromTime(rawCreatedAt)

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(401, gin.H{"error": "invalid credentials"})
		return
	}

	// Generate token
	token, err := generateToken(u.ID, u.Email, service.JWTSecret)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to generate token"})
		return
	}

	// Create sync session
	syncSessionID, err := createSyncSession(c.Request.Context(), database, u.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create sync session"})
		return
	}

	c.JSON(200, AuthResponse{Token: token, SyncSessionID: syncSessionID, User: u})
}

type LogoutRequest struct {
	SyncSessionID *uuid.UUID `json:"sync_session_id"`
}

func Logout(c *gin.Context, service *AuthService) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	userID, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.SyncSessionID != nil {
		_, err := service.DB.Pool.Exec(c.Request.Context(),
			`UPDATE sync_sessions SET invalidated_at = now(), invalidation_reason = 'logout'
			 WHERE id = $1 AND user_id = $2`,
			*req.SyncSessionID, userID,
		)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to invalidate sync session"})
			return
		}
	}

	c.JSON(200, gin.H{"message": "logged out successfully"})
}

func DeleteMe(c *gin.Context, service *AuthService) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	userID, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	_, err := service.DB.Pool.Exec(c.Request.Context(), "DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete user"})
		return
	}

	c.JSON(200, gin.H{"message": "user deleted successfully"})
}

func createSyncSession(ctx context.Context, database *db.DB, userID uuid.UUID) (uuid.UUID, error) {
	var sessionID uuid.UUID
	err := database.Pool.QueryRow(ctx,
		`INSERT INTO sync_sessions (user_id) VALUES ($1) RETURNING id`,
		userID,
	).Scan(&sessionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create sync session: %w", err)
	}
	return sessionID, nil
}

func generateToken(userID uuid.UUID, email string, jwtSecret string) (string, error) {
	claims := user.Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(jwtSecret))
}
