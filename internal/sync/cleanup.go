package sync

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartSessionCleanup runs a background goroutine that periodically invalidates
// sync sessions older than ttlDays. It stops when the context is cancelled.
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
					log.Printf("[SYNC] session cleanup expired %d sessions (ttl=%d days)", expired, ttlDays)
				}
			}
		}
	}()
	log.Printf("✓ Sync session cleanup started (TTL=%d days)", ttlDays)
}

func expireSessions(ctx context.Context, database *db.DB, ttlDays int) (int64, error) {
	tag, err := database.Pool.Exec(ctx,
		`UPDATE sync_sessions
		 SET invalidated_at = now(), invalidation_reason = 'expired'
		 WHERE invalidated_at IS NULL
		   AND created_at < now() - MAKE_INTERVAL(days => $1)`,
		ttlDays,
	)
	if err != nil {
		return 0, fmt.Errorf("expire sync sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
