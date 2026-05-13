// Package testutil provides shared test helpers for integration tests:
// DB setup with migrations, table truncation, and fixture factories.
// Import this package only from _test.go files.
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// SetupDB connects to the test database, runs all migrations, and returns a
// *db.DB. The caller is responsible for calling db.Close() when done.
//
// Database URL resolution order:
//  1. TEST_DATABASE_URL env var
//  2. DATABASE_URL env var
//  3. default postgres://postgres:postgres@localhost:5432/finance_manager_test?sslmode=disable
func SetupDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/finance_manager_test?sslmode=disable"
	}

	err := db.RunMigrations(context.Background(), dbURL, "")
	require.NoError(t, err)

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)

	return &db.DB{Pool: pool}
}

// TruncateAll resets all application tables to a clean state. Call this at
// the start of each test (or sub-test) to ensure isolation.
func TruncateAll(t *testing.T, database *db.DB) {
	t.Helper()
	_, err := database.Pool.Exec(context.Background(),
		"TRUNCATE users, groups, group_members, group_transactions, group_transaction_splits, settlements, transactions CASCADE")
	require.NoError(t, err)
}

// TruncateUsers truncates only the users table (cascades to most other tables).
func TruncateUsers(t *testing.T, database *db.DB) {
	t.Helper()
	_, err := database.Pool.Exec(context.Background(), "TRUNCATE users CASCADE")
	require.NoError(t, err)
}

// TruncateTables truncates specific tables by name (with CASCADE). Use when
// you need finer-grained control than TruncateAll.
func TruncateTables(t *testing.T, database *db.DB, tables ...string) {
	t.Helper()
	for _, table := range tables {
		_, err := database.Pool.Exec(context.Background(), "TRUNCATE "+table+" CASCADE")
		require.NoError(t, err)
	}
}

// CreateUser inserts a user row with a bcrypt-hashed password and returns the
// generated user ID. currency defaults to "INR" and timezone to "UTC".
func CreateUser(t *testing.T, database *db.DB, email, username, password string) uuid.UUID {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)

	id := uuid.New()
	_, err = database.Pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)`,
		id, email, username, string(hash))
	require.NoError(t, err)
	return id
}

// CreateGroup inserts a group owned by creatorID and adds the creator as the
// first member, mirroring the handler behaviour. Returns the group ID.
func CreateGroup(t *testing.T, database *db.DB, name string, creatorID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO groups (id, name, created_by) VALUES ($1, $2, $3)`,
		id, name, creatorID)
	require.NoError(t, err)

	_, err = database.Pool.Exec(context.Background(),
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`,
		id, creatorID)
	require.NoError(t, err)
	return id
}

// AddGroupMember adds userID as a member of groupID.
func AddGroupMember(t *testing.T, database *db.DB, groupID, userID uuid.UUID) {
	t.Helper()
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`,
		groupID, userID)
	require.NoError(t, err)
}

// CreateSettlement inserts a settlement row directly (bypasses handler logic —
// use for balance-calculation tests, not handler behaviour tests).
func CreateSettlement(t *testing.T, database *db.DB, groupID, fromUser, toUser uuid.UUID, amount decimal.Decimal) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO settlements (id, group_id, from_user, to_user, amount) VALUES ($1, $2, $3, $4, $5)`,
		id, groupID, fromUser, toUser, amount)
	require.NoError(t, err)
	return id
}
