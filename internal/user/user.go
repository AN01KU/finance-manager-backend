package user

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
)

type User struct {
	ID            uuid.UUID           `json:"id" db:"id"`
	Email         string              `json:"email" db:"email"`
	Username      string              `json:"username" db:"username"`
	PasswordHash  string              `json:"-" db:"password_hash"`
	Currency      string              `json:"currency" db:"currency"`
	EmailVerified bool                `json:"email_verified" db:"email_verified"`
	CreatedAt     helpers.EpochMillis `json:"created_at" db:"created_at"`
}

// Claims holds JWT token claims for authenticated users.
type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
	jwt.RegisteredClaims
}
