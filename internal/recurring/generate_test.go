package recurring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func intPtr(v int) *int { return &v }

func TestNextOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		startDate  time.Time
		frequency  string
		dayOfMonth *int
		daysOfWeek []int
		today      time.Time
		want       *time.Time
	}{
		{
			name:      "daily — returns today",
			startDate: date(2026, 1, 1),
			frequency: "daily",
			today:     date(2026, 4, 10),
			want:      timePtr(date(2026, 4, 10)),
		},
		{
			name:      "daily — future start returns nil",
			startDate: date(2026, 5, 1),
			frequency: "daily",
			today:     date(2026, 4, 10),
			want:      nil,
		},
		{
			name:       "monthly — day 15, today is 15th",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(15),
			today:      date(2026, 4, 15),
			want:       timePtr(date(2026, 4, 15)),
		},
		{
			name:       "monthly — day 15, today is 14th (before this month's)",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(15),
			today:      date(2026, 4, 14),
			want:       timePtr(date(2026, 3, 15)),
		},
		{
			// Last-valid-day-of-month semantics: day=31 fires on Mar 31 (not Apr 31
			// which doesn't exist). Today (Apr 28) is before Apr's resolved fire
			// date of Apr 30, so the latest due date is Mar 31.
			name:       "monthly — day 31, today Apr 28 → Mar 31 (last valid prior)",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2026, 4, 28),
			want:       timePtr(date(2026, 3, 31)),
		},
		{
			// day=31 in a 30-day month resolves to the 30th.
			name:       "monthly — day 31, today Apr 30 → Apr 30 (clamped)",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2026, 4, 30),
			want:       timePtr(date(2026, 4, 30)),
		},
		{
			// day=31 in February (non-leap) → 28th.
			name:       "monthly — day 31 in February → Feb 28",
			startDate:  date(2026, 1, 31),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2026, 2, 28),
			want:       timePtr(date(2026, 2, 28)),
		},
		{
			// day=29 in February of a leap year → 29th.
			name:       "monthly — day 29 in leap February → Feb 29",
			startDate:  date(2024, 1, 29),
			frequency:  "monthly",
			dayOfMonth: intPtr(29),
			today:      date(2024, 2, 29),
			want:       timePtr(date(2024, 2, 29)),
		},
		{
			name:       "monthly — no dayOfMonth, advances by month",
			startDate:  date(2026, 1, 10),
			frequency:  "monthly",
			dayOfMonth: nil,
			today:      date(2026, 4, 10),
			want:       timePtr(date(2026, 4, 10)),
		},
		{
			// Regression: startDate after this month's dayOfMonth — first valid
			// occurrence is in the future, so nothing is due.
			name:       "monthly — startDate Apr 25, day 23, today Apr 25 → nil",
			startDate:  date(2026, 4, 25),
			frequency:  "monthly",
			dayOfMonth: intPtr(23),
			today:      date(2026, 4, 25),
			want:       nil,
		},
		{
			// Regression: startDate before this month's dayOfMonth — must snap
			// to dayOfMonth, not return startDate itself.
			name:       "monthly — startDate Apr 1, day 23, today Apr 25 → Apr 23",
			startDate:  date(2026, 4, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(23),
			today:      date(2026, 4, 25),
			want:       timePtr(date(2026, 4, 23)),
		},
		{
			// Regression: startDate after dayOfMonth in start month — first
			// valid occurrence is dayOfMonth of the next month.
			name:       "monthly — startDate Apr 24, day 23, today May 24 → May 23",
			startDate:  date(2026, 4, 24),
			frequency:  "monthly",
			dayOfMonth: intPtr(23),
			today:      date(2026, 5, 24),
			want:       timePtr(date(2026, 5, 23)),
		},
		{
			// Crossing year boundary while snapping forward.
			name:       "monthly — startDate Dec 25, day 5, today Jan 10 → Jan 5",
			startDate:  date(2025, 12, 25),
			frequency:  "monthly",
			dayOfMonth: intPtr(5),
			today:      date(2026, 1, 10),
			want:       timePtr(date(2026, 1, 5)),
		},
		{
			name:      "weekly — no daysOfWeek, advances by 7",
			startDate: date(2026, 1, 6), // Tuesday
			frequency: "weekly",
			today:     date(2026, 1, 20), // Tuesday
			want:      timePtr(date(2026, 1, 20)),
		},
		{
			name:       "weekly — with daysOfWeek",
			startDate:  date(2026, 1, 1), // Thursday
			frequency:  "weekly",
			daysOfWeek: []int{1, 3},               // Monday, Wednesday
			today:      date(2026, 1, 8),          // Thursday
			want:       timePtr(date(2026, 1, 7)), // Wednesday Jan 7
		},
		{
			name:       "weekly — daysOfWeek, no match yet",
			startDate:  date(2026, 5, 1), // future
			frequency:  "weekly",
			daysOfWeek: []int{0}, // Sunday
			today:      date(2026, 4, 10),
			want:       nil,
		},
		{
			name:       "weekly — long range (3 years) stays O(1)",
			startDate:  date(2023, 1, 2), // Monday, 3 years ago
			frequency:  "weekly",
			daysOfWeek: []int{1, 5},       // Monday, Friday
			today:      date(2026, 4, 10), // Friday → matches daysOfWeek
			want:       timePtr(date(2026, 4, 10)),
		},
		{
			name:      "yearly — same day",
			startDate: date(2024, 4, 10),
			frequency: "yearly",
			today:     date(2026, 4, 10),
			want:      timePtr(date(2026, 4, 10)),
		},
		{
			name:      "yearly — not yet this year",
			startDate: date(2024, 6, 15),
			frequency: "yearly",
			today:     date(2026, 4, 10),
			want:      timePtr(date(2025, 6, 15)),
		},
		{
			name:      "unknown frequency returns nil",
			startDate: date(2026, 1, 1),
			frequency: "biweekly",
			today:     date(2026, 4, 10),
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextOccurrence(tt.startDate, tt.frequency, tt.dayOfMonth, tt.daysOfWeek, tt.today)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func TestNextFutureOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		startDate  time.Time
		frequency  string
		dayOfMonth *int
		daysOfWeek []int
		today      time.Time
		want       *time.Time
	}{
		{
			name:      "daily — next day",
			startDate: date(2026, 1, 1),
			frequency: "daily",
			today:     date(2026, 4, 10),
			want:      timePtr(date(2026, 4, 11)),
		},
		{
			name:       "monthly — day 1, today is Apr 2 → May 1",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(1),
			today:      date(2026, 4, 2),
			want:       timePtr(date(2026, 5, 1)),
		},
		{
			// Regression: startDate before this month's dayOfMonth and dayOfMonth
			// itself is still in the future today — must return this month, not next.
			name:       "monthly — startDate Apr 1, day 23, today Apr 20 → Apr 23",
			startDate:  date(2026, 4, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(23),
			today:      date(2026, 4, 20),
			want:       timePtr(date(2026, 4, 23)),
		},
		{
			// Regression: startDate after this month's dayOfMonth — must skip to
			// next month's dayOfMonth.
			name:       "monthly — startDate Apr 25, day 23, today Apr 25 → May 23",
			startDate:  date(2026, 4, 25),
			frequency:  "monthly",
			dayOfMonth: intPtr(23),
			today:      date(2026, 4, 25),
			want:       timePtr(date(2026, 5, 23)),
		},
		{
			name:      "yearly — next year",
			startDate: date(2024, 3, 1),
			frequency: "yearly",
			today:     date(2026, 4, 10),
			want:      timePtr(date(2027, 3, 1)),
		},
		{
			name:      "unknown frequency returns nil",
			startDate: date(2026, 1, 1),
			frequency: "never",
			today:     date(2026, 4, 10),
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextFutureOccurrence(tt.startDate, tt.frequency, tt.dayOfMonth, tt.daysOfWeek, tt.today)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func TestAllMissedOccurrences(t *testing.T) {
	tests := []struct {
		name       string
		startDate  time.Time
		frequency  string
		dayOfMonth *int
		daysOfWeek []int
		lastAdded  *time.Time
		today      time.Time
		want       []time.Time
	}{
		{
			name:      "daily — 5 day backfill from start",
			startDate: date(2026, 4, 1),
			frequency: "daily",
			today:     date(2026, 4, 5),
			want: []time.Time{
				date(2026, 4, 1), date(2026, 4, 2), date(2026, 4, 3),
				date(2026, 4, 4), date(2026, 4, 5),
			},
		},
		{
			name:      "daily — resumes strictly after lastAdded",
			startDate: date(2026, 4, 1),
			frequency: "daily",
			lastAdded: timePtr(date(2026, 4, 3)),
			today:     date(2026, 4, 5),
			want:      []time.Time{date(2026, 4, 4), date(2026, 4, 5)},
		},
		{
			name:      "daily — already up to date returns empty",
			startDate: date(2026, 4, 1),
			frequency: "daily",
			lastAdded: timePtr(date(2026, 4, 5)),
			today:     date(2026, 4, 5),
			want:      nil,
		},
		{
			name:       "monthly day 31 — Jan-Apr backfill yields last-valid days",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2026, 4, 30),
			want: []time.Time{
				date(2026, 1, 31), date(2026, 2, 28),
				date(2026, 3, 31), date(2026, 4, 30),
			},
		},
		{
			name:       "monthly day 31 — leap year February yields Feb 29",
			startDate:  date(2024, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2024, 3, 31),
			want: []time.Time{
				date(2024, 1, 31), date(2024, 2, 29), date(2024, 3, 31),
			},
		},
		{
			name:       "weekly daysOfWeek=[1,3,5] — 2 week backfill",
			startDate:  date(2026, 1, 5), // Monday
			frequency:  "weekly",
			daysOfWeek: []int{1, 3, 5}, // Mon, Wed, Fri
			today:      date(2026, 1, 16),
			want: []time.Time{
				date(2026, 1, 5), date(2026, 1, 7), date(2026, 1, 9),
				date(2026, 1, 12), date(2026, 1, 14), date(2026, 1, 16),
			},
		},
		{
			name:      "weekly no daysOfWeek — every 7 days from start",
			startDate: date(2026, 1, 1),
			frequency: "weekly",
			today:     date(2026, 1, 22),
			want: []time.Time{
				date(2026, 1, 1), date(2026, 1, 8),
				date(2026, 1, 15), date(2026, 1, 22),
			},
		},
		{
			name:      "yearly — multi-year backfill",
			startDate: date(2023, 4, 10),
			frequency: "yearly",
			today:     date(2026, 4, 10),
			want: []time.Time{
				date(2023, 4, 10), date(2024, 4, 10),
				date(2025, 4, 10), date(2026, 4, 10),
			},
		},
		{
			name:      "yearly Feb 29 — non-leap years clamp to Feb 28",
			startDate: date(2024, 2, 29),
			frequency: "yearly",
			today:     date(2027, 2, 28),
			want: []time.Time{
				date(2024, 2, 29), date(2025, 2, 28),
				date(2026, 2, 28), date(2027, 2, 28),
			},
		},
		{
			name:      "future startDate — nothing yet",
			startDate: date(2026, 5, 1),
			frequency: "daily",
			today:     date(2026, 4, 30),
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allMissedOccurrences(tt.startDate, tt.frequency, tt.dayOfMonth, tt.daysOfWeek, tt.lastAdded, tt.today, time.UTC)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllMissedOccurrences_TimezoneAware(t *testing.T) {
	ist, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("Asia/Kolkata tzdata not available")
	}

	// Rule starts Apr 1 at IST midnight; "now" is Apr 5 17:00 UTC
	// (= Apr 5 22:30 IST). Expect 5 IST days: Apr 1..5 at IST midnight.
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, ist)
	now := time.Date(2026, 4, 5, 17, 0, 0, 0, time.UTC)

	got := allMissedOccurrences(start, "daily", nil, nil, nil, now, ist)
	if assert.Len(t, got, 5) {
		for i, d := range got {
			expected := time.Date(2026, 4, 1+i, 0, 0, 0, 0, ist)
			assert.True(t, d.Equal(expected), "got=%s want=%s", d, expected)
		}
	}
}

func TestStartOfDay(t *testing.T) {
	input := time.Date(2026, 4, 10, 15, 30, 45, 123, time.UTC)
	got := startOfDay(input)
	assert.Equal(t, date(2026, 4, 10), got)
}

func TestContainsInt(t *testing.T) {
	assert.True(t, containsInt([]int{1, 3, 5}, 3))
	assert.False(t, containsInt([]int{1, 3, 5}, 4))
	assert.False(t, containsInt([]int{}, 0))
}

func timePtr(t time.Time) *time.Time { return &t }
