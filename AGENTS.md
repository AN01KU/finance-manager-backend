# AGENTS.md

Guidance for AI agents (Codex, Claude, etc.) working in this repository.

## Pre-Commit Checklist

**Never commit without running these first. No exceptions.**

```bash
# 1. Format
gofmt -s -w .

# 2. Build
go build ./...

# 3. Vet
go vet ./...

# 4. Unit tests
go test ./internal/middleware/... ./internal/config/... ./internal/recurring/... ./internal/helpers/...
```

All four must pass cleanly before committing. If any fail, fix them first.

## Repo Notes

- See CLAUDE.md for full architecture, environment variables, and domain rules.
- Migrations run automatically on startup — no manual step needed.
- All monetary amounts use `shopspring/decimal.Decimal` internally.
- Soft delete vs hard delete (`is_deleted` flag):
  - `groups`, `group_transactions`, `settlements` → **soft delete** (cross-user audit trail matters).
  - Personal `transactions` rows linked to a group_transaction or settlement → **soft delete via cascade** (when the parent is deleted or a member is removed from a split).
  - Personal `transactions` rows the user deletes directly via `DELETE /transactions/:id` → **hard delete** (single-user data; client handles undo/rollback).
  - Soft-deleted `transactions`, `group_transactions`, `settlements`, and `groups` are all hard-deleted after 1 day by the `StartSoftDeleteCleanup` goroutine.
  - Read paths must always filter `is_deleted = FALSE` on tables that use the flag.
- Integration tests require a Postgres DB and must call `db.RunMigrations` in test setup.
