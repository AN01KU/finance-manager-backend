package transaction

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartSoftDeleteCleanup runs a background goroutine that:
//   - hard-deletes soft-deleted transactions/groups older than 1 day
//   - hard-deletes soft-deleted custom_category tombstones older than tombstoneRetentionDays
//
// It stops when the context is cancelled.
func StartSoftDeleteCleanup(ctx context.Context, database *db.DB, tombstoneRetentionDays int) {
	logger := slog.Default().With("job", "transaction_soft_delete_cleanup")
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		// Run once immediately on startup, then on each tick.
		if err := purgeSoftDeleted(ctx, database, logger); err != nil {
			logger.Error("soft-delete cleanup error", applog.KeyError, err)
		}
		if err := purgeCategoryTombstones(ctx, database, tombstoneRetentionDays, logger); err != nil {
			logger.Error("category tombstone purge error", applog.KeyError, err)
		}
		for {
			select {
			case <-ctx.Done():
				logger.Info("soft-delete cleanup stopped")
				return
			case <-ticker.C:
				if err := purgeSoftDeleted(ctx, database, logger); err != nil {
					logger.Error("soft-delete cleanup error", applog.KeyError, err)
				}
				if err := purgeCategoryTombstones(ctx, database, tombstoneRetentionDays, logger); err != nil {
					logger.Error("category tombstone purge error", applog.KeyError, err)
				}
			}
		}
	}()
	logger.Info("transaction soft-delete cleanup started",
		"ttl_days", 1,
		"category_tombstone_retention_days", tombstoneRetentionDays,
	)
}

func purgeCategoryTombstones(ctx context.Context, database *db.DB, retentionDays int, logger *slog.Logger) error {
	tag, err := database.Pool.Exec(ctx,
		`DELETE FROM custom_categories
		 WHERE is_deleted = TRUE
		   AND updated_at < NOW() - MAKE_INTERVAL(days => $1)`,
		retentionDays,
	)
	if err != nil {
		return fmt.Errorf("purge category tombstones: %w", err)
	}
	if tag.RowsAffected() > 0 {
		logger.Info("purged category tombstones", "count", tag.RowsAffected(), "older_than_days", retentionDays)
	}
	return nil
}

func purgeSoftDeleted(ctx context.Context, database *db.DB, logger *slog.Logger) error {
	// Order: delete the most-dependent tables first so FK CASCADE / SET NULL
	// doesn't fight us on shared rows.
	//   transactions     → may reference settlements / group_transactions / groups (SET NULL on FK)
	//   group_transactions → references groups (CASCADE on FK)
	//   settlements      → references groups (CASCADE on FK)
	//   groups           → top-level
	txTag, err := database.Pool.Exec(ctx,
		`DELETE FROM transactions
		 WHERE is_deleted = TRUE
		   AND updated_at < NOW() - INTERVAL '1 day'`,
	)
	if err != nil {
		return fmt.Errorf("purge soft-deleted transactions: %w", err)
	}

	gtTag, err := database.Pool.Exec(ctx,
		`DELETE FROM group_transactions
		 WHERE is_deleted = TRUE
		   AND updated_at < NOW() - INTERVAL '1 day'`,
	)
	if err != nil {
		return fmt.Errorf("purge soft-deleted group_transactions: %w", err)
	}

	settleTag, err := database.Pool.Exec(ctx,
		`DELETE FROM settlements
		 WHERE is_deleted = TRUE
		   AND updated_at < NOW() - INTERVAL '1 day'`,
	)
	if err != nil {
		return fmt.Errorf("purge soft-deleted settlements: %w", err)
	}

	groupTag, err := database.Pool.Exec(ctx,
		`DELETE FROM groups
		 WHERE is_deleted = TRUE
		   AND updated_at < NOW() - INTERVAL '1 day'`,
	)
	if err != nil {
		return fmt.Errorf("purge soft-deleted groups: %w", err)
	}

	txCount := txTag.RowsAffected()
	gtCount := gtTag.RowsAffected()
	settleCount := settleTag.RowsAffected()
	groupCount := groupTag.RowsAffected()
	if txCount > 0 || gtCount > 0 || settleCount > 0 || groupCount > 0 {
		logger.Info("purged soft-deleted rows",
			"transactions", txCount,
			"group_transactions", gtCount,
			"settlements", settleCount,
			"groups", groupCount,
		)
	}
	return nil
}
