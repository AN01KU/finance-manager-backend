package helpers

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolveCategoryKey validates that key is visible to userID and returns it
// unchanged. If the key is not found (deleted, hidden, or belongs to another
// user) it returns "other" so transactions always land in a valid bucket.
//
// Resolution order:
//  1. predefined_categories — key exists and is not hidden (global, no user scope)
//  2. custom_categories — key exists, owned by userID, not predefined, not hidden
//  3. fallback → "other"
func ResolveCategoryKey(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, key string) (string, error) {
	if key == "other" {
		return "other", nil
	}

	var found bool
	err := pool.QueryRow(ctx, `
		SELECT TRUE FROM predefined_categories
		WHERE key = $1 AND is_hidden = FALSE
		UNION ALL
		SELECT TRUE FROM custom_categories
		WHERE key = $1 AND user_id = $2 AND is_predefined = FALSE AND is_hidden = FALSE
		LIMIT 1`,
		key, userID,
	).Scan(&found)

	if err != nil || !found {
		return "other", nil
	}
	return key, nil
}
