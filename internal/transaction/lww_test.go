package transaction

import (
	"testing"
	"time"
)

func TestIsStaleWrite(t *testing.T) {
	base := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		existingUpdatedAt time.Time
		incomingUpdatedAt time.Time
		wantStale         bool
	}{
		{
			name:              "incoming older than existing — stale, reject",
			existingUpdatedAt: base.Add(time.Second),
			incomingUpdatedAt: base,
			wantStale:         true,
		},
		{
			name:              "incoming newer than existing — fresh, accept",
			existingUpdatedAt: base,
			incomingUpdatedAt: base.Add(time.Second),
			wantStale:         false,
		},
		{
			name:              "identical timestamps — treated as fresh, accept",
			existingUpdatedAt: base,
			incomingUpdatedAt: base,
			wantStale:         false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsStaleWrite(tc.existingUpdatedAt, tc.incomingUpdatedAt)
			if got != tc.wantStale {
				t.Errorf("IsStaleWrite(%v, %v) = %v, want %v",
					tc.existingUpdatedAt, tc.incomingUpdatedAt, got, tc.wantStale)
			}
		})
	}
}
