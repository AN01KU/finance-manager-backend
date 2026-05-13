package db

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/finance_manager_test?sslmode=disable"
	}

	require.NoError(t, RunMigrations(context.Background(), dbURL, ""))

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestWithTx_CommitOnSuccess(t *testing.T) {
	pool := testPool(t)

	// Ensure the table exists and is clean for this test
	_, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS _withtx_test (val INT)`)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `DELETE FROM _withtx_test`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _withtx_test`)
	})

	err = WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO _withtx_test VALUES (42)`)
		return err
	})
	require.NoError(t, err)

	var count int
	err = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM _withtx_test WHERE val = 42`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "row should be committed")
}

func TestWithTx_RollbackOnFnError(t *testing.T) {
	pool := testPool(t)

	_, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS _withtx_test (val INT)`)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `DELETE FROM _withtx_test`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _withtx_test`)
	})

	sentinel := errors.New("deliberate failure")
	err = WithTx(context.Background(), pool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(context.Background(), `INSERT INTO _withtx_test VALUES (99)`)
		if execErr != nil {
			return execErr
		}
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel, "caller should receive the fn error")

	var count int
	err = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM _withtx_test WHERE val = 99`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "row should be rolled back")
}

func TestWithTx_ErrorReturnedToCaller(t *testing.T) {
	pool := testPool(t)

	want := errors.New("specific error")
	got := WithTx(context.Background(), pool, func(_ pgx.Tx) error {
		return want
	})
	assert.ErrorIs(t, got, want)
}
