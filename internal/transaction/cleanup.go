package transaction

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartSoftDeleteCleanup runs a background goroutine that:
//   - hard-deletes soft-deleted transactions/groups older than 1 day
//   - hard-deletes soft-deleted custom_category tombstones older than tombstoneRetentionDays
//
// It stops when the context is cancelled.
func StartSoftDeleteCleanup(ctx context.Context, database *db.DB, tombstoneRetentionDays int) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		// Run once immediately on startup, then on each tick.
		if err := purgeSoftDeleted(ctx, database); err != nil {
			log.Printf("[TRANSACTION] soft-delete cleanup error: %v", err)
		}
		if err := purgeCategoryTombstones(ctx, database, tombstoneRetentionDays); err != nil {
			log.Printf("[TRANSACTION] category tombstone purge error: %v", err)
		}
		for {
			select {
			case <-ctx.Done():
				log.Println("[TRANSACTION] soft-delete cleanup stopped")
				return
			case <-ticker.C:
				if err := purgeSoftDeleted(ctx, database); err != nil {
					log.Printf("[TRANSACTION] soft-delete cleanup error: %v", err)
				}
				if err := purgeCategoryTombstones(ctx, database, tombstoneRetentionDays); err != nil {
					log.Printf("[TRANSACTION] category tombstone purge error: %v", err)
				}
			}
		}
	}()
	log.Printf("✓ Transaction soft-delete cleanup started (TTL=1 day, category tombstones=%d days)", tombstoneRetentionDays)
}

func purgeCategoryTombstones(ctx context.Context, database *db.DB, retentionDays int) error {
	tag, err := database.Pool.Exec(ctx,
		`DELETE FROM custom_categories
		 WHERE deleted_at IS NOT NULL
		   AND deleted_at < NOW() - MAKE_INTERVAL(days => $1)`,
		retentionDays,
	)
	if err != nil {
		return fmt.Errorf("purge category tombstones: %w", err)
	}
	if tag.RowsAffected() > 0 {
		log.Printf("[TRANSACTION] purged %d category tombstones (>%d days old)", tag.RowsAffected(), retentionDays)
	}
	return nil
}

func purgeSoftDeleted(ctx context.Context, database *db.DB) error {
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
		log.Printf("[TRANSACTION] purged %d transactions, %d group_transactions, %d settlements, %d groups",
			txCount, gtCount, settleCount, groupCount)
	}
	return nil
}
