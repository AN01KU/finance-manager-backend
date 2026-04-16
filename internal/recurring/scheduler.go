package recurring

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartBackgroundGeneration runs a background goroutine that periodically
// generates due recurring transactions for all users. It stops when the
// context is cancelled.
func StartBackgroundGeneration(ctx context.Context, database *db.DB) {
	ticker := time.NewTicker(15 * time.Minute)
	go func() {
		defer ticker.Stop()
		// Run once at startup.
		generateAll(ctx, database)
		for {
			select {
			case <-ctx.Done():
				log.Println("[RECURRING] background generation stopped")
				return
			case <-ticker.C:
				generateAll(ctx, database)
			}
		}
	}()
	log.Println("✓ Recurring transaction background generation started (every 15m)")
}

func generateAll(ctx context.Context, database *db.DB) {
	rows, err := database.Pool.Query(ctx,
		`SELECT DISTINCT user_id FROM recurring_transactions WHERE is_active = true`)
	if err != nil {
		log.Printf("[RECURRING] failed to query active users: %v", err)
		return
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			log.Printf("[RECURRING] failed to scan user_id: %v", err)
			return
		}
		userIDs = append(userIDs, uid)
	}
	rows.Close()

	now := time.Now()
	for _, uid := range userIDs {
		if err := GenerateDueTransactions(ctx, uid, database, now); err != nil {
			log.Printf("[RECURRING] generate for user %s: %v", uid, err)
		}
	}
}
