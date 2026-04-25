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
		dow := make([]int, len(r.daysOfWeek))
		for i, v := range r.daysOfWeek {
			dow[i] = int(v)
		}
		next := nextOccurrence(r.startDate, r.frequency, r.dayOfMonth, dow, today)
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
		tx, err := database.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for recurring %s: %w", r.id, err)
		}

		_, err = tx.Exec(ctx,
			`INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
			 ON CONFLICT (user_id, recurring_transaction_id, date) WHERE recurring_transaction_id IS NOT NULL DO NOTHING`,
			txID, userID, r.txType, r.amount, r.category, *next, r.name, nil, r.id,
		)
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("insert transaction for recurring %s: %w", r.id, err)
		}

		// Update last_added_date in the same transaction so insert + date update are atomic.
		_, err = tx.Exec(ctx,
			`UPDATE recurring_transactions
			 SET last_added_date = GREATEST(COALESCE(last_added_date, '-infinity'::timestamptz), $1),
			     updated_at = NOW()
			 WHERE id = $2`,
			*next, r.id,
		)
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("update last_added_date for recurring %s: %w", r.id, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tx for recurring %s: %w", r.id, err)
		}
	}

	return nil
}

// nextOccurrence returns the latest occurrence of the recurring rule that falls
// on or before today. GenerateDueTransactions uses this to create transactions
// only for dates that are already due — never for future dates.
func nextOccurrence(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, today time.Time) *time.Time {
	next := startOfDay(startDate)

	// If the start date is in the future, nothing is due yet.
	if next.After(today) {
		return nil
	}

	switch frequency {
	case "daily":
		// The latest daily occurrence on or before today is simply today.
		next = today

	case "weekly":
		if len(daysOfWeek) == 0 {
			// Jump forward in bulk: calculate weeks between start and today.
			days := int(today.Sub(next).Hours() / 24)
			weeks := days / 7
			next = next.AddDate(0, 0, weeks*7)
		} else {
			// Walk backwards from today (at most 7 days) to find the latest matching weekday.
			var latest *time.Time
			for i := 0; i < 7; i++ {
				candidate := today.AddDate(0, 0, -i)
				if candidate.Before(next) {
					break
				}
				if containsInt(daysOfWeek, int(candidate.Weekday())) {
					t := candidate
					latest = &t
					break
				}
			}
			if latest == nil {
				return nil
			}
			next = *latest
		}

	case "monthly":
		if dayOfMonth == nil {
			for next.AddDate(0, 1, 0).Before(today) || next.AddDate(0, 1, 0).Equal(today) {
				next = next.AddDate(0, 1, 0)
			}
		} else {
			day := *dayOfMonth
			if day > 28 {
				day = 28
			}
			// Find the latest month where day_of_month falls on or before today.
			for {
				y, m, _ := next.Date()
				m++
				if m > 12 {
					m = 1
					y++
				}
				candidate := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
				if candidate.After(today) {
					break
				}
				next = candidate
			}
		}

	case "yearly":
		for next.AddDate(1, 0, 0).Before(today) || next.AddDate(1, 0, 0).Equal(today) {
			next = next.AddDate(1, 0, 0)
		}

	default:
		return nil
	}

	return &next
}

// nextFutureOccurrence returns the next occurrence strictly after today.
// Used for the API response's next_occurrence field.
func nextFutureOccurrence(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, today time.Time) *time.Time {
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
				weekday := int(next.Weekday())
				if containsInt(daysOfWeek, weekday) && next.After(today) {
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

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
