package user

import (
	"github.com/google/uuid"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
)

type User struct {
	ID           uuid.UUID           `json:"id" db:"id"`
	Email        string              `json:"email" db:"email"`
	Username     string              `json:"username" db:"username"`
	PasswordHash string              `json:"-" db:"password_hash"`
	CreatedAt    helpers.EpochMillis `json:"created_at" db:"created_at"`
}
