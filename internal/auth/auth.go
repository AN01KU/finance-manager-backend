package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/email"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
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
	DB          *db.DB
	JWTSecret   string
	InviteCode  string
	EmailClient *email.Client
	OnSignup    func(ctx context.Context, userID uuid.UUID) // called after user creation
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
		"INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id, email, username, currency, email_verified, created_at",
		req.Email, req.Username, string(hash)).Scan(&u.ID, &u.Email, &u.Username, &u.Currency, &u.EmailVerified, &rawCreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create user"})
		return
	}
	u.CreatedAt = helpers.FromTime(rawCreatedAt)

	// Run post-signup hook (e.g., seed predefined categories)
	if service.OnSignup != nil {
		service.OnSignup(c.Request.Context(), u.ID)
	}

	// Send verification email
	sendVerificationEmail(c.Request.Context(), service, u.ID, u.Email)

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
		"SELECT id, email, username, currency, email_verified, created_at FROM users WHERE id = $1", userID).Scan(
		&u.ID, &u.Email, &u.Username, &u.Currency, &u.EmailVerified, &rawCreatedAt)
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
		"SELECT id, email, username, password_hash, currency, email_verified, created_at FROM users WHERE email = $1", req.Email).Scan(
		&u.ID, &u.Email, &u.Username, &u.PasswordHash, &u.Currency, &u.EmailVerified, &rawCreatedAt)
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

	// Invalidate any existing active sessions before creating a new one
	invalidateAllSessions(c.Request.Context(), database, u.ID, "new_login")

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
		// Invalidate the specific session
		_, err := service.DB.Pool.Exec(c.Request.Context(),
			`UPDATE sync_sessions SET invalidated_at = now(), invalidation_reason = 'logout'
			 WHERE id = $1 AND user_id = $2 AND invalidated_at IS NULL`,
			*req.SyncSessionID, userID,
		)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to invalidate sync session"})
			return
		}
	} else {
		// No specific session provided — invalidate all active sessions
		invalidateAllSessions(c.Request.Context(), service.DB, userID, "logout")
	}

	c.JSON(200, gin.H{"message": "logged out successfully"})
}

type UpdateMeRequest struct {
	Username *string `json:"username,omitempty" validate:"omitempty,min=3"`
	Email    *string `json:"email,omitempty" validate:"omitempty,email"`
	Password *string `json:"password,omitempty" validate:"omitempty,min=6"`
	Currency *string `json:"currency,omitempty" validate:"omitempty,len=3"`
}

func UpdateMe(c *gin.Context, service *AuthService) {
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

	var req UpdateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Username == nil && req.Email == nil && req.Password == nil && req.Currency == nil {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query := `UPDATE users SET`
	args := []interface{}{}
	n := 1

	if req.Username != nil {
		query += fmt.Sprintf(` username = $%d,`, n)
		args = append(args, *req.Username)
		n++
	}

	if req.Email != nil {
		// Check email uniqueness
		var count int
		err := service.DB.Pool.QueryRow(c.Request.Context(),
			"SELECT COUNT(*) FROM users WHERE email = $1 AND id != $2",
			*req.Email, userID).Scan(&count)
		if err != nil {
			c.JSON(500, gin.H{"error": "database error"})
			return
		}
		if count > 0 {
			c.JSON(409, gin.H{"error": "email already in use"})
			return
		}
		query += fmt.Sprintf(` email = $%d,`, n)
		args = append(args, *req.Email)
		n++
	}

	if req.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to hash password"})
			return
		}
		query += fmt.Sprintf(` password_hash = $%d,`, n)
		args = append(args, string(hash))
		n++
	}

	if req.Currency != nil {
		query += fmt.Sprintf(` currency = $%d,`, n)
		args = append(args, *req.Currency)
		n++
	}

	// Add updated_at and remove trailing comma, then add WHERE + RETURNING
	query += ` updated_at = NOW()`
	query += fmt.Sprintf(` WHERE id = $%d RETURNING id, email, username, currency, email_verified, created_at`, n)
	args = append(args, userID)

	var u user.User
	var rawCreatedAt time.Time
	err := service.DB.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&u.ID, &u.Email, &u.Username, &u.Currency, &u.EmailVerified, &rawCreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update user"})
		return
	}
	u.CreatedAt = helpers.FromTime(rawCreatedAt)

	// Password or email change — invalidate all sync sessions to force re-login
	if req.Password != nil || req.Email != nil {
		invalidateAllSessions(c.Request.Context(), service.DB, userID, "credentials_changed")
	}

	c.JSON(200, u)
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

	// Check if user is the creator of any active groups
	var ownedGroupCount int
	err := service.DB.Pool.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM groups WHERE created_by = $1 AND is_deleted = FALSE", userID,
	).Scan(&ownedGroupCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if ownedGroupCount > 0 {
		c.JSON(400, gin.H{"error": "cannot delete account while you are the creator of active groups; delete your groups first"})
		return
	}

	// Check if user has non-zero balance in any active group
	rows, err := service.DB.Pool.Query(c.Request.Context(),
		`SELECT gm.group_id FROM group_members gm
		 JOIN groups g ON gm.group_id = g.id
		 WHERE gm.user_id = $1 AND g.is_deleted = FALSE`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var groupID uuid.UUID
		if err := rows.Scan(&groupID); err != nil {
			c.JSON(500, gin.H{"error": "database error"})
			return
		}
		balance, err := helpers.GetUserGroupBalance(c.Request.Context(), service.DB, groupID, userID)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to calculate group balance"})
			return
		}
		if !balance.IsZero() {
			c.JSON(400, gin.H{"error": "cannot delete account with non-zero group balances; settle up and leave all groups first"})
			return
		}
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}

	_, err = service.DB.Pool.Exec(c.Request.Context(), "DELETE FROM users WHERE id = $1", userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete user"})
		return
	}

	c.JSON(200, gin.H{"message": "user deleted successfully"})
}

func invalidateAllSessions(ctx context.Context, database *db.DB, userID uuid.UUID, reason string) {
	_, _ = database.Pool.Exec(ctx,
		`UPDATE sync_sessions SET invalidated_at = now(), invalidation_reason = $1
		 WHERE user_id = $2 AND invalidated_at IS NULL`,
		reason, userID,
	)
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

// ---------------------------------------------------------------------------
// Email Verification
// ---------------------------------------------------------------------------

const verificationCodeExpiry = 10 * time.Minute

type VerifyEmailRequest struct {
	Code string `json:"code" validate:"required,len=6"`
}

// generateCode returns a cryptographically random 6-digit code.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// sendVerificationEmail creates a code and sends it. Best-effort — errors are logged, not returned.
func sendVerificationEmail(ctx context.Context, service *AuthService, userID uuid.UUID, emailAddr string) {
	code, err := generateCode()
	if err != nil {
		fmt.Printf("[AUTH] failed to generate verification code: %v\n", err)
		return
	}

	_, err = service.DB.Pool.Exec(ctx,
		`INSERT INTO email_verifications (user_id, code, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, code, time.Now().Add(verificationCodeExpiry),
	)
	if err != nil {
		fmt.Printf("[AUTH] failed to store verification code: %v\n", err)
		return
	}

	if service.EmailClient != nil {
		if err := service.EmailClient.SendVerificationCode(emailAddr, code); err != nil {
			fmt.Printf("[AUTH] failed to send verification email: %v\n", err)
		}
	}
}

// VerifyEmail validates a verification code and marks the user's email as verified.
func VerifyEmail(c *gin.Context, service *AuthService) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Check if already verified
	var alreadyVerified bool
	err := service.DB.Pool.QueryRow(c.Request.Context(),
		"SELECT email_verified FROM users WHERE id = $1", userID,
	).Scan(&alreadyVerified)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if alreadyVerified {
		c.JSON(400, gin.H{"error": "email already verified"})
		return
	}

	// Find valid, unused code
	var codeID uuid.UUID
	err = service.DB.Pool.QueryRow(c.Request.Context(),
		`SELECT id FROM email_verifications
		 WHERE user_id = $1 AND code = $2 AND used_at IS NULL AND expires_at > NOW()
		 ORDER BY created_at DESC LIMIT 1`,
		userID, req.Code,
	).Scan(&codeID)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid or expired verification code"})
		return
	}

	// Mark code as used and verify user — atomic
	tx, err := service.DB.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	_, err = tx.Exec(c.Request.Context(),
		`UPDATE email_verifications SET used_at = NOW() WHERE id = $1`, codeID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to mark code as used"})
		return
	}

	_, err = tx.Exec(c.Request.Context(),
		`UPDATE users SET email_verified = TRUE, updated_at = NOW() WHERE id = $1`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to verify email"})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(200, gin.H{"message": "email verified successfully"})
}

// ResendVerification generates and sends a new verification code.
func ResendVerification(c *gin.Context, service *AuthService) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var emailAddr string
	var alreadyVerified bool
	err := service.DB.Pool.QueryRow(c.Request.Context(),
		"SELECT email, email_verified FROM users WHERE id = $1", userID,
	).Scan(&emailAddr, &alreadyVerified)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if alreadyVerified {
		c.JSON(400, gin.H{"error": "email already verified"})
		return
	}

	// Rate limit: only allow a new code if the last one was sent more than 60 seconds ago
	var recentCount int
	err = service.DB.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM email_verifications
		 WHERE user_id = $1 AND created_at > NOW() - INTERVAL '60 seconds'`,
		userID,
	).Scan(&recentCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if recentCount > 0 {
		c.JSON(429, gin.H{"error": "please wait before requesting a new code"})
		return
	}

	sendVerificationEmail(c.Request.Context(), service, userID, emailAddr)

	c.JSON(200, gin.H{"message": "verification code sent"})
}
