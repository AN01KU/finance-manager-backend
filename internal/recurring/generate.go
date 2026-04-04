package recurring

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// GenerateDueTransactions checks all active recurring transactions for the given
// user and inserts any overdue transaction instances, updating last_added_date.
// It is called on GET /transactions so the list is always up-to-date.
func GenerateDueTransactions(ctx context.Context, userID uuid.UUID, database *db.DB, now time.Time) error {
	today := startOfDay(now)

	rows, err := database.Pool.Query(ctx,
		`SELECT id, type, name, amount, category, frequency, day_of_month, days_of_week,
		        start_date, end_date, last_added_date
		 FROM recurring_transactions
		 WHERE user_id = $1 AND is_active = true`,
		userID)
	if err != nil {
		return fmt.Errorf("query recurring: %w", err)
	}
	defer rows.Close()

	type recurringRow struct {
		id            uuid.UUID
		txType        string
		name          string
		amount        decimal.Decimal
		category      string
		frequency     string
		dayOfMonth    *int
		daysOfWeek    []int32
		startDate     time.Time
		endDate       *time.Time
		lastAddedDate *time.Time
	}

	var recs []recurringRow
	for rows.Next() {
		var r recurringRow
		if err := rows.Scan(
			&r.id, &r.txType, &r.name, &r.amount, &r.category,
			&r.frequency, &r.dayOfMonth, &r.daysOfWeek,
			&r.startDate, &r.endDate, &r.lastAddedDate,
		); err != nil {
			return fmt.Errorf("scan recurring: %w", err)
		}
		recs = append(recs, r)
	}
	rows.Close()

	for _, r := range recs {
		next := nextOccurrence(r.startDate, r.frequency, r.dayOfMonth, r.daysOfWeek, today)
		if next == nil {
			continue
		}

		// Skip if already generated up to this date
		if r.lastAddedDate != nil {
			lastDay := startOfDay(*r.lastAddedDate)
			if !next.After(lastDay) {
				continue
			}
		}

		// Respect end date
		if r.endDate != nil && next.After(*r.endDate) {
			continue
		}

		txID := uuid.New()
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
			 ON CONFLICT DO NOTHING`,
			txID, userID, r.txType, r.amount, r.category, *next, r.name, nil, r.id,
		)
		if err != nil {
			return fmt.Errorf("insert transaction for recurring %s: %w", r.id, err)
		}

		_, err = database.Pool.Exec(ctx,
			`UPDATE recurring_transactions SET last_added_date = $1, updated_at = NOW() WHERE id = $2`,
			*next, r.id,
		)
		if err != nil {
			return fmt.Errorf("update last_added_date for recurring %s: %w", r.id, err)
		}
	}

	return nil
}

// nextOccurrence mirrors the iOS RecurringDateHelper.nextOccurrence logic.
// It returns the next date strictly after today that this recurring rule fires.
func nextOccurrence(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int32, today time.Time) *time.Time {
	next := startOfDay(startDate)

	switch frequency {
	case "daily":
		for !next.After(today) {
			next = next.AddDate(0, 0, 1)
		}

	case "weekly":
		if len(daysOfWeek) == 0 {
			for !next.After(today) {
				next = next.AddDate(0, 0, 7)
			}
		} else {
			for !next.After(today) {
				// iOS: adjustedWeekday = weekday - 1, where Sunday=1 → 0, Monday=2 → 1
				weekday := int(next.Weekday()) // Go: Sunday=0, Monday=1
				if containsInt32(daysOfWeek, int32(weekday)) && next.After(today) {
					break
				}
				next = next.AddDate(0, 0, 1)
			}
		}

	case "monthly":
		if dayOfMonth == nil {
			for !next.After(today) {
				next = next.AddDate(0, 1, 0)
			}
		} else {
			day := *dayOfMonth
			if day > 28 {
				day = 28
			}
			for !next.After(today) {
				y, m, _ := next.Date()
				// Advance to next month, then set the day
				m++
				if m > 12 {
					m = 1
					y++
				}
				next = time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
			}
		}

	case "yearly":
		for !next.After(today) {
			next = next.AddDate(1, 0, 0)
		}

	default:
		return nil
	}

	return &next
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func containsInt32(slice []int32, val int32) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
