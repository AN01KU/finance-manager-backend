//go:build release

package seed

import (
	"context"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// Seed is a no-op in release builds.
func Seed(_ context.Context, _ *db.DB) error { return nil }

// Cleanup is a no-op in release builds.
func Cleanup(_ context.Context, _ *db.DB) {}
