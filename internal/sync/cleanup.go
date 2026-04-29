package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// hardDeleteAfterDays controls how long invalidated sync_sessions rows linger
// before being hard-deleted from the table. Without this the table grows
// unbounded over time (one row per login event per user, forever).
const hardDeleteAfterDays = 180

// StartSessionCleanup runs a background goroutine that:
//   - invalidates sessions whose last_seen_at is older than ttlDays (rolling
//     idle timeout — a continuously-syncing client never expires);
//   - hard-deletes invalidated rows older than hardDeleteAfterDays so the
//     table doesn't grow forever.
//
// It stops when the context is cancelled.
func StartSessionCleanup(ctx context.Context, database *db.DB, ttlDays int) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("[SYNC] session cleanup stopped")
				return
			case <-ticker.C:
				expired, err := expireSessions(ctx, database, ttlDays)
				if err != nil {
					log.Printf("[SYNC] session cleanup error: %v", err)
				} else if expired > 0 {
					log.Printf("[SYNC] session cleanup expired %d sessions (idle ttl=%d days)", expired, ttlDays)
				}
				deleted, err := purgeInvalidatedSessions(ctx, database, hardDeleteAfterDays)
				if err != nil {
					log.Printf("[SYNC] session purge error: %v", err)
				} else if deleted > 0 {
					log.Printf("[SYNC] session purge hard-deleted %d invalidated rows (>%d days old)", deleted, hardDeleteAfterDays)
				}
			}
		}
	}()
	log.Printf("✓ Sync session cleanup started (idle TTL=%d days, hard-delete after=%d days)", ttlDays, hardDeleteAfterDays)
}

// expireSessions invalidates sessions whose last_seen_at is older than
// ttlDays. Using last_seen_at (refreshed by SyncSessionGuard on every
// authenticated mutating request) gives a rolling idle timeout: an actively-
// syncing client never expires regardless of how long ago they first logged in.
func expireSessions(ctx context.Context, database *db.DB, ttlDays int) (int64, error) {
	tag, err := database.Pool.Exec(ctx,
		`UPDATE sync_sessions
		 SET invalidated_at = now(), invalidation_reason = 'expired'
		 WHERE invalidated_at IS NULL
		   AND last_seen_at < now() - MAKE_INTERVAL(days => $1)`,
		ttlDays,
	)
	if err != nil {
		return 0, fmt.Errorf("expire sync sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// purgeInvalidatedSessions hard-deletes sync_sessions rows that were
// invalidated more than retentionDays ago. Keeps the table size bounded
// without losing recent audit history.
func purgeInvalidatedSessions(ctx context.Context, database *db.DB, retentionDays int) (int64, error) {
	tag, err := database.Pool.Exec(ctx,
		`DELETE FROM sync_sessions
		 WHERE invalidated_at IS NOT NULL
		   AND invalidated_at < now() - MAKE_INTERVAL(days => $1)`,
		retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("purge invalidated sync sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
