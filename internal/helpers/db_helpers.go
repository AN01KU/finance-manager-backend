package helpers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// GetUserGroupBalance computes a user's net balance in a group.
// Positive = owed money (paid more), Negative = owes money.
func GetUserGroupBalance(ctx context.Context, database *db.DB, groupID, userID uuid.UUID) (decimal.Decimal, error) {
	balance := decimal.Zero

	// Amount paid by user
	var paid decimal.NullDecimal
	err := database.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(total_amount), 0) FROM group_transactions
		 WHERE group_id = $1 AND paid_by_user_id = $2 AND is_deleted = FALSE`,
		groupID, userID).Scan(&paid)
	if err != nil {
		return balance, err
	}
	if paid.Valid {
		balance = balance.Add(paid.Decimal)
	}

	// Amount owed (splits)
	var splits decimal.NullDecimal
	err = database.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(gts.amount), 0)
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gt.group_id = $1 AND gts.user_id = $2 AND gt.is_deleted = FALSE`,
		groupID, userID).Scan(&splits)
	if err != nil {
		return balance, err
	}
	if splits.Valid {
		balance = balance.Sub(splits.Decimal)
	}

	// Settlements: from_user paid off debt (+), to_user received (-)
	var settPaid decimal.NullDecimal
	err = database.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM settlements
		 WHERE group_id = $1 AND from_user = $2`,
		groupID, userID).Scan(&settPaid)
	if err != nil {
		return balance, err
	}
	if settPaid.Valid {
		balance = balance.Add(settPaid.Decimal)
	}

	var settReceived decimal.NullDecimal
	err = database.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM settlements
		 WHERE group_id = $1 AND to_user = $2`,
		groupID, userID).Scan(&settReceived)
	if err != nil {
		return balance, err
	}
	if settReceived.Valid {
		balance = balance.Sub(settReceived.Decimal)
	}

	return balance, nil
}

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
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return userID, true, nil
}
