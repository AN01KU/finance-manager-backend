package recurring

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// GenerateDueTransactions checks all active recurring transactions for the
// given user and inserts EVERY missed transaction instance between the rule's
// last fire (or start_date) and `now`, evaluated in the user's timezone.
//
// Idempotency:
//   - Inserts use ON CONFLICT (user_id, recurring_transaction_id, date) DO
//     NOTHING so a re-run never duplicates.
//   - Dates present in recurring_skipped_occurrences (populated when the user
//     manually deletes a generated instance) are filtered out so the user's
//     intentional deletes are respected.
//
// Called by the scheduler tick and on demand from GET /transactions.
func GenerateDueTransactions(ctx context.Context, userID uuid.UUID, database *db.DB, now time.Time) error {
	loc, err := loadUserTimezone(ctx, database, userID)
	if err != nil {
		return err
	}
	today := startOfDayIn(now, loc)

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

		dates := allMissedOccurrences(r.startDate, r.frequency, r.dayOfMonth, dow, r.lastAddedDate, today, loc)
		if r.endDate != nil {
			endDay := startOfDayIn(*r.endDate, loc)
			filtered := dates[:0]
			for _, d := range dates {
				if !d.After(endDay) {
					filtered = append(filtered, d)
				}
			}
			dates = filtered
		}
		if len(dates) == 0 {
			continue
		}

		// Filter out dates the user previously deleted manually.
		skipped, err := loadSkippedDates(ctx, database, r.id)
		if err != nil {
			return fmt.Errorf("load skipped for recurring %s: %w", r.id, err)
		}
		var toGenerate []time.Time
		for _, d := range dates {
			if _, isSkipped := skipped[d.Format("2006-01-02")]; isSkipped {
				continue
			}
			toGenerate = append(toGenerate, d)
		}
		if len(toGenerate) == 0 {
			continue
		}

		tx, err := database.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for recurring %s: %w", r.id, err)
		}

		for _, d := range toGenerate {
			txID := uuid.New()
			_, err = tx.Exec(ctx,
				`INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, updated_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
				 ON CONFLICT (user_id, recurring_transaction_id, date) WHERE recurring_transaction_id IS NOT NULL DO NOTHING`,
				txID, userID, r.txType, r.amount, r.category, d, r.name, nil, r.id,
			)
			if err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("insert transaction for recurring %s: %w", r.id, err)
			}
		}

		// Bump last_added_date to the latest generated date so the next tick
		// starts strictly after it. GREATEST guards against rare out-of-order
		// concurrent inserts (e.g. on-demand generation racing the scheduler).
		latest := toGenerate[len(toGenerate)-1]
		_, err = tx.Exec(ctx,
			`UPDATE recurring_transactions
			 SET last_added_date = GREATEST(COALESCE(last_added_date, '-infinity'::timestamptz), $1),
			     updated_at = NOW()
			 WHERE id = $2`,
			latest, r.id,
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

// loadUserTimezone looks up the user's IANA timezone and resolves it to a
// *time.Location. Falls back to UTC if the user row is missing or the stored
// name is unparseable (e.g. tzdata change). Never blocks generation.
func loadUserTimezone(ctx context.Context, database *db.DB, userID uuid.UUID) (*time.Location, error) {
	var tzName string
	err := database.Pool.QueryRow(ctx,
		`SELECT timezone FROM users WHERE id = $1`, userID,
	).Scan(&tzName)
	if err != nil {
		// User not found / DB error → degrade to UTC, don't break recurring.
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil || loc == nil {
		return time.UTC, nil
	}
	return loc, nil
}

func loadSkippedDates(ctx context.Context, database *db.DB, recurringID uuid.UUID) (map[string]struct{}, error) {
	rows, err := database.Pool.Query(ctx,
		`SELECT occurrence_date FROM recurring_skipped_occurrences WHERE recurring_transaction_id = $1`,
		recurringID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d.Format("2006-01-02")] = struct{}{}
	}
	return out, rows.Err()
}

// allMissedOccurrences returns every date the rule should fire that is on or
// before `today`, strictly after `lastAdded` (if set), and on or after
// `startDate`. All comparisons are in `loc` (user's timezone).
//
// Empty `loc` is treated as UTC.
func allMissedOccurrences(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, lastAdded *time.Time, today time.Time, loc *time.Location) []time.Time {
	if loc == nil {
		loc = time.UTC
	}
	startDay := startOfDayIn(startDate, loc)
	todayDay := startOfDayIn(today, loc)
	if startDay.After(todayDay) {
		return nil
	}

	cursor := startDay
	if lastAdded != nil {
		la := startOfDayIn(*lastAdded, loc)
		if !la.Before(cursor) {
			// Resume strictly after the last fire date.
			cursor = la.AddDate(0, 0, 1)
		}
	}
	if cursor.After(todayDay) {
		return nil
	}

	switch frequency {
	case "daily":
		return collectDaily(cursor, todayDay)
	case "weekly":
		if len(daysOfWeek) == 0 {
			return collectWeeklyAligned(startDay, cursor, todayDay)
		}
		return collectWeeklyByDay(cursor, todayDay, daysOfWeek)
	case "monthly":
		td := startDay.Day()
		if dayOfMonth != nil {
			td = *dayOfMonth
		}
		return collectMonthly(cursor, todayDay, td, loc)
	case "yearly":
		return collectYearly(cursor, todayDay, startDay.Month(), startDay.Day(), loc)
	default:
		return nil
	}
}

func collectDaily(cursor, today time.Time) []time.Time {
	var out []time.Time
	for !cursor.After(today) {
		out = append(out, cursor)
		cursor = cursor.AddDate(0, 0, 1)
	}
	return out
}

// collectWeeklyAligned fires every 7 days from startDay. Cursor may have
// advanced past lastAdded — snap it forward to the next valid grid date.
func collectWeeklyAligned(startDay, cursor, today time.Time) []time.Time {
	diffDays := int(cursor.Sub(startDay).Hours() / 24)
	if mod := diffDays % 7; mod != 0 {
		cursor = cursor.AddDate(0, 0, 7-mod)
	}
	var out []time.Time
	for !cursor.After(today) {
		out = append(out, cursor)
		cursor = cursor.AddDate(0, 0, 7)
	}
	return out
}

func collectWeeklyByDay(cursor, today time.Time, daysOfWeek []int) []time.Time {
	var out []time.Time
	for !cursor.After(today) {
		if containsInt(daysOfWeek, int(cursor.Weekday())) {
			out = append(out, cursor)
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	return out
}

// collectMonthly fires on `targetDay` of each month, using last-valid-day-of-month
// semantics: targetDay=31 in February becomes the 28th/29th, in April becomes
// the 30th, etc. This matches Splitwise / Money Lover behavior, not RFC 5545
// (which would skip those months entirely).
func collectMonthly(cursor, today time.Time, targetDay int, loc *time.Location) []time.Time {
	var out []time.Time
	y, m, _ := cursor.Date()
	for {
		fire := resolveMonthDay(y, m, targetDay, loc)
		if fire.Before(cursor) {
			y, m = advanceMonth(y, m)
			continue
		}
		if fire.After(today) {
			return out
		}
		out = append(out, fire)
		y, m = advanceMonth(y, m)
	}
}

func collectYearly(cursor, today time.Time, anniversaryMonth time.Month, anniversaryDay int, loc *time.Location) []time.Time {
	var out []time.Time
	y, _, _ := cursor.Date()
	for {
		fire := resolveMonthDay(y, anniversaryMonth, anniversaryDay, loc)
		if fire.Before(cursor) {
			y++
			continue
		}
		if fire.After(today) {
			return out
		}
		out = append(out, fire)
		y++
	}
}

// resolveMonthDay returns midnight of (year, month, min(day, lastDayOfMonth)).
// time.Date(year, month+1, 0, ...) normalizes to the last day of `month`.
func resolveMonthDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day < 1 {
		day = 1
	}
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

func advanceMonth(y int, m time.Month) (int, time.Month) {
	m++
	if m > 12 {
		m = 1
		y++
	}
	return y, m
}

// nextOccurrence returns the latest occurrence of the rule that falls on or
// before today (single-shot variant kept for backwards compatibility with
// callers that want a "what's the most recent due date" answer).
//
// Internally this just runs the full backfill enumeration and returns the
// last element so day-of-month / leap-year semantics stay consistent.
func nextOccurrence(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, today time.Time) *time.Time {
	dates := allMissedOccurrences(startDate, frequency, dayOfMonth, daysOfWeek, nil, today, time.UTC)
	if len(dates) == 0 {
		return nil
	}
	last := dates[len(dates)-1]
	return &last
}

// NextFutureOccurrence returns the next occurrence strictly after `today`.
// Used for the API response's next_occurrence field. UTC-only — the response
// is informational and doesn't drive generation.
func NextFutureOccurrence(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, today time.Time) *time.Time {
	return nextOccurrenceAfter(startDate, frequency, dayOfMonth, daysOfWeek, today, time.UTC)
}

// nextOccurrenceAfter returns the first occurrence strictly greater than
// `after`, in the given location. Bounded search: walks at most ~400 days for
// daily/weekly, ~13 months for monthly, ~5 years for yearly before giving up.
func nextOccurrenceAfter(startDate time.Time, frequency string, dayOfMonth *int, daysOfWeek []int, after time.Time, loc *time.Location) *time.Time {
	if loc == nil {
		loc = time.UTC
	}
	startDay := startOfDayIn(startDate, loc)
	afterDay := startOfDayIn(after, loc)

	// Probe horizon — comfortably covers the largest valid interval.
	horizon := afterDay.AddDate(5, 0, 0)
	if startDay.After(horizon) {
		return nil
	}

	cursor := startDay
	if cursor.Before(afterDay) || cursor.Equal(afterDay) {
		// Start enumeration from the day after `after`.
		cursor = afterDay.AddDate(0, 0, 1)
	}

	switch frequency {
	case "daily":
		return &cursor
	case "weekly":
		if len(daysOfWeek) == 0 {
			diffDays := int(cursor.Sub(startDay).Hours() / 24)
			if mod := diffDays % 7; mod != 0 {
				cursor = cursor.AddDate(0, 0, 7-mod)
			}
			if cursor.After(horizon) {
				return nil
			}
			return &cursor
		}
		for i := 0; i < 14; i++ {
			if containsInt(daysOfWeek, int(cursor.Weekday())) {
				return &cursor
			}
			cursor = cursor.AddDate(0, 0, 1)
		}
		return nil
	case "monthly":
		td := startDay.Day()
		if dayOfMonth != nil {
			td = *dayOfMonth
		}
		y, m, _ := cursor.Date()
		for i := 0; i < 14; i++ {
			fire := resolveMonthDay(y, m, td, loc)
			if !fire.Before(cursor) {
				return &fire
			}
			y, m = advanceMonth(y, m)
		}
		return nil
	case "yearly":
		y, _, _ := cursor.Date()
		for i := 0; i < 6; i++ {
			fire := resolveMonthDay(y, startDay.Month(), startDay.Day(), loc)
			if !fire.Before(cursor) {
				return &fire
			}
			y++
		}
		return nil
	default:
		return nil
	}
}

func startOfDayIn(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	in := t.In(loc)
	y, m, d := in.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// startOfDay is kept for backwards compatibility with callers (and tests) that
// don't care about timezone — it always returns UTC midnight.
func startOfDay(t time.Time) time.Time {
	return startOfDayIn(t, time.UTC)
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
