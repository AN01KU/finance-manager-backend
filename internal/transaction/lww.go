package transaction

import "time"

// IsStaleWrite reports whether an incoming write should be rejected because the
// server already holds a strictly newer version of the row.
//
// Tie-breaking (equal timestamps): treated as a fresh write so the update
// proceeds. This matches the SQL guard `transactions.updated_at <=
// EXCLUDED.updated_at`, which lets equal-timestamp upserts through.
func IsStaleWrite(existingUpdatedAt, incomingUpdatedAt time.Time) bool {
	return existingUpdatedAt.After(incomingUpdatedAt)
}
