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
			name:       "monthly — day 31 clamped to 28",
			startDate:  date(2026, 1, 1),
			frequency:  "monthly",
			dayOfMonth: intPtr(31),
			today:      date(2026, 4, 28),
			want:       timePtr(date(2026, 4, 28)),
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
			daysOfWeek: []int{1, 3}, // Monday, Wednesday
			today:      date(2026, 1, 8), // Thursday
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
			daysOfWeek: []int{1, 5}, // Monday, Friday
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
			got := nextFutureOccurrence(tt.startDate, tt.frequency, tt.dayOfMonth, tt.daysOfWeek, tt.today)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
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
