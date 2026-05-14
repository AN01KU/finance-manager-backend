package recurring

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// StartBackgroundGeneration runs a background goroutine that periodically
// generates due recurring transactions for all users. It stops when the
// context is cancelled.
func StartBackgroundGeneration(ctx context.Context, database *db.DB) {
	logger := slog.Default().With("job", "recurring_generation")
	ticker := time.NewTicker(15 * time.Minute)
	go func() {
		defer ticker.Stop()
		// Run once at startup.
		generateAll(ctx, database, logger)
		for {
			select {
			case <-ctx.Done():
				logger.Info("background generation stopped")
				return
			case <-ticker.C:
				generateAll(ctx, database, logger)
			}
		}
	}()
	logger.Info("recurring transaction background generation started", "interval", "15m")
}

func generateAll(ctx context.Context, database *db.DB, logger *slog.Logger) {
	rows, err := database.Pool.Query(ctx,
		`SELECT DISTINCT user_id FROM recurring_transactions WHERE is_active = true`)
	if err != nil {
		logger.Error("failed to query active users", applog.KeyError, err)
		return
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			logger.Error("failed to scan user_id", applog.KeyError, err)
			return
		}
		userIDs = append(userIDs, uid)
	}
	rows.Close()

	now := time.Now()
	for _, uid := range userIDs {
		if err := GenerateDueTransactions(ctx, uid, database, now); err != nil {
			logger.Error("recurring generate for user failed", applog.KeyUserID, uid, applog.KeyError, err)
		}
	}
}
