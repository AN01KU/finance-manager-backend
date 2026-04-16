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
- Expenses use soft delete (`is_deleted` flag) — always filter `is_deleted = FALSE`.
- Integration tests require a Postgres DB and must call `db.RunMigrations` in test setup.
