package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
)

// ReminderConfig holds thresholds for settlement reminders.
type ReminderConfig struct {
	// ThresholdAmount is the minimum absolute balance (in the user's currency)
	// that must be outstanding before a reminder is sent. Zero disables reminders.
	ThresholdAmount decimal.Decimal
	// DaysOutstanding is the minimum number of days the balance must have been
	// sitting without any new activity in the group before a reminder fires.
	DaysOutstanding int
}

// shouldSendReminder returns true when a user with the given balance should
// receive a settlement reminder. It is a pure function with no side effects.
//
// Rules:
//   - Only debtors (negative balance) are reminded — creditors are not.
//   - The absolute balance must be >= threshold (zero threshold disables reminders).
//   - The balance must have been outstanding for at least minDays days.
func shouldSendReminder(balance, threshold decimal.Decimal, daysOutstanding, minDays int) bool {
	if threshold.IsZero() {
		return false
	}
	if !balance.IsNegative() {
		return false
	}
	absBalance := balance.Abs()
	if absBalance.LessThan(threshold) {
		return false
	}
	return daysOutstanding >= minDays
}

// StartSettlementReminders runs a background goroutine that checks all groups
// once per day and sends push notifications to users with overdue balances.
// It stops when ctx is cancelled.
func StartSettlementReminders(ctx context.Context, database *db.DB, push *Client, cfg ReminderConfig) {
	logger := slog.Default().With("job", "settlement_reminders")
	if !push.Enabled() {
		logger.Info("push notifications disabled — settlement reminders inactive")
		return
	}
	if cfg.ThresholdAmount.IsZero() {
		logger.Info("threshold is zero — settlement reminders inactive")
		return
	}

	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		// Run once at startup.
		sendOverdueReminders(ctx, database, push, cfg, logger)
		for {
			select {
			case <-ctx.Done():
				logger.Info("settlement reminder job stopped")
				return
			case <-ticker.C:
				sendOverdueReminders(ctx, database, push, cfg, logger)
			}
		}
	}()
	logger.Info("settlement reminders started",
		"threshold", cfg.ThresholdAmount.String(),
		"min_days", cfg.DaysOutstanding,
		"interval", "24h",
	)
}

// sendOverdueReminders queries all active groups, computes per-member balances,
// and fires a reminder push for any member that meets the threshold criteria.
func sendOverdueReminders(ctx context.Context, database *db.DB, push *Client, cfg ReminderConfig, logger *slog.Logger) {
	rows, err := database.Pool.Query(ctx,
		`SELECT DISTINCT gm.group_id, g.name
		 FROM group_members gm
		 JOIN groups g ON gm.group_id = g.id
		 WHERE g.is_deleted = FALSE`)
	if err != nil {
		logger.Error("failed to query groups", applog.KeyError, err)
		return
	}

	type groupRow struct {
		id   uuid.UUID
		name string
	}
	var groups []groupRow
	for rows.Next() {
		var gr groupRow
		if err := rows.Scan(&gr.id, &gr.name); err != nil {
			logger.Error("failed to scan group", applog.KeyError, err)
			rows.Close()
			return
		}
		groups = append(groups, gr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		logger.Error("group query error", applog.KeyError, err)
		return
	}

	now := time.Now()

	for _, g := range groups {
		// Determine last activity in the group (newest transaction or settlement).
		var lastActivity time.Time
		err := database.Pool.QueryRow(ctx,
			`SELECT COALESCE(
				GREATEST(
					(SELECT MAX(created_at) FROM group_transactions WHERE group_id = $1 AND is_deleted = FALSE),
					(SELECT MAX(created_at) FROM settlements WHERE group_id = $1 AND is_deleted = FALSE)
				),
				'-infinity'::timestamptz
			)`, g.id).Scan(&lastActivity)
		if err != nil {
			logger.Error("failed to get last activity", applog.KeyGroupID, g.id, applog.KeyError, err)
			continue
		}

		daysOutstanding := int(now.Sub(lastActivity).Hours() / 24)

		// Fetch all members of the group.
		memberRows, err := database.Pool.Query(ctx,
			`SELECT user_id FROM group_members WHERE group_id = $1`, g.id)
		if err != nil {
			logger.Error("failed to get members", applog.KeyGroupID, g.id, applog.KeyError, err)
			continue
		}

		var memberIDs []uuid.UUID
		for memberRows.Next() {
			var uid uuid.UUID
			if err := memberRows.Scan(&uid); err != nil {
				logger.Error("failed to scan member", applog.KeyGroupID, g.id, applog.KeyError, err)
				memberRows.Close()
				break
			}
			memberIDs = append(memberIDs, uid)
		}
		memberRows.Close()
		if err := memberRows.Err(); err != nil {
			logger.Error("member query error", applog.KeyGroupID, g.id, applog.KeyError, err)
			continue
		}

		for _, uid := range memberIDs {
			balance, err := helpers.GetUserGroupBalance(ctx, database, g.id, uid)
			if err != nil {
				logger.Error("failed to get balance", applog.KeyUserID, uid, applog.KeyGroupID, g.id, applog.KeyError, err)
				continue
			}

			if !shouldSendReminder(balance, cfg.ThresholdAmount, daysOutstanding, cfg.DaysOutstanding) {
				continue
			}

			absBalance := balance.Abs()
			push.SendToUser(ctx, uid, map[string]any{
				"type":     "settlement_reminder",
				"group_id": g.id.String(),
				"amount":   absBalance.String(),
			}, &IOSNotification{
				Title: fmt.Sprintf("You owe %s in %s", formatCurrency(absBalance), g.name),
				Body:  "Settle up to keep your balance clear.",
				Sound: "default",
			})

			logger.Info("sent settlement reminder",
				applog.KeyUserID, uid,
				applog.KeyGroupID, g.id,
				"balance", balance.String(),
				"days_outstanding", daysOutstanding,
			)
		}
	}
}

// formatCurrency formats a decimal as a currency string (e.g. "42.50").
func formatCurrency(amount decimal.Decimal) string {
	return "$" + amount.StringFixed(2)
}
