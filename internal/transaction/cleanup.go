package transaction

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartSoftDeleteCleanup runs a background goroutine that hard-deletes soft-deleted
// transactions (and their parent group_transactions) older than 1 day.
// It stops when the context is cancelled.
func StartSoftDeleteCleanup(ctx context.Context, database *db.DB) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		// Run once immediately on startup, then on each tick.
		if err := purgeSoftDeleted(ctx, database); err != nil {
			log.Printf("[TRANSACTION] soft-delete cleanup error: %v", err)
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
			}
		}
	}()
	log.Println("✓ Transaction soft-delete cleanup started (TTL=1 day)")
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
