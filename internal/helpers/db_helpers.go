package helpers

import (
	"context"

	"github.com/google/uuid"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// IsGroupMember checks if a user is a member of a group
func IsGroupMember(ctx context.Context, db *db.DB, groupID, userID uuid.UUID) (bool, error) {
	var isMember bool
	err := db.Pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)",
		groupID, userID).Scan(&isMember)
	return isMember, err
}

// UserExists checks if a user exists by ID
func UserExists(ctx context.Context, db *db.DB, userID uuid.UUID) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)",
		userID).Scan(&exists)
	return exists, err
}

// GetUserByEmail gets a user by email
func GetUserByEmail(ctx context.Context, db *db.DB, email string) (uuid.UUID, bool, error) {
	var userID uuid.UUID
	err := db.Pool.QueryRow(ctx,
		"SELECT id FROM users WHERE email = $1",
		email).Scan(&userID)
	if err != nil {
		return uuid.Nil, false, nil
	}
	return userID, true, nil
}
