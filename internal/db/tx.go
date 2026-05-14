package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx begins a transaction, calls fn with it, commits on nil return, and
// rolls back on any error (including panics). Callers never need to call
// Begin, Rollback, or Commit directly.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (retErr error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if retErr != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
