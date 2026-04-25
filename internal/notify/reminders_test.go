package notify

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestShouldSendReminder(t *testing.T) {
	tests := []struct {
		name            string
		balance         decimal.Decimal // negative = user owes money
		threshold       decimal.Decimal // abs threshold, e.g. 20.00
		daysOutstanding int             // days since last activity in the group
		minDays         int             // configured minimum days before reminding
		want            bool
	}{
		{
			name:            "owes above threshold, stale enough — send",
			balance:         decimal.NewFromFloat(-50),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 8,
			minDays:         7,
			want:            true,
		},
		{
			name:            "owes above threshold, not stale enough — skip",
			balance:         decimal.NewFromFloat(-50),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 3,
			minDays:         7,
			want:            false,
		},
		{
			name:            "owes below threshold — skip",
			balance:         decimal.NewFromFloat(-10),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 30,
			minDays:         7,
			want:            false,
		},
		{
			name:            "balance is zero — skip",
			balance:         decimal.Zero,
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 30,
			minDays:         7,
			want:            false,
		},
		{
			name:            "positive balance (creditor) — skip, only debtors get reminded",
			balance:         decimal.NewFromFloat(50),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 30,
			minDays:         7,
			want:            false,
		},
		{
			name:            "owes exactly threshold — send",
			balance:         decimal.NewFromFloat(-20),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 7,
			minDays:         7,
			want:            true,
		},
		{
			name:            "owes just below threshold — skip",
			balance:         decimal.NewFromFloat(-19.99),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 30,
			minDays:         7,
			want:            false,
		},
		{
			name:            "exactly at minDays boundary — send",
			balance:         decimal.NewFromFloat(-100),
			threshold:       decimal.NewFromFloat(20),
			daysOutstanding: 7,
			minDays:         7,
			want:            true,
		},
		{
			name:            "zero threshold disables reminders — skip",
			balance:         decimal.NewFromFloat(-1000),
			threshold:       decimal.Zero,
			daysOutstanding: 30,
			minDays:         7,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSendReminder(tt.balance, tt.threshold, tt.daysOutstanding, tt.minDays)
			assert.Equal(t, tt.want, got, "balance=%s threshold=%s days=%d minDays=%d",
				tt.balance, tt.threshold, tt.daysOutstanding, tt.minDays)
		})
	}
}
