# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

## Operational Constraints

- **Production must run with `GIN_MODE=release`.** This enables the `Secure` flag on admin session cookies, activates rate limiting on `/auth/*`, disables the SQL runner, and skips seed data. Running in debug mode in production will expose the SQL runner and set insecure cookies.
- **The server must be started from the repo root directory.** The admin dashboard templates are loaded at startup via a relative path (`internal/admin/templates/`). Starting from any other directory will cause template parsing to fail at boot.
- **Recurring transaction backfill:** the scheduler (`GenerateDueTransactions`) fires today-only — it never back-fills missed dates. To generate transactions for dates already passed, use `POST /admin/recurring/:id/backfill` from the admin panel. This uses the same `MissedOccurrences` algorithm as the scheduler's overdue detection but inserts one row per missed date with ON CONFLICT DO NOTHING.

## Commands

```bash
# Run the server
go run ./cmd/main.go

# Run with hot reload (requires air: go install github.com/cosmtrek/air@latest)
air

# Build
go build -o finance-manager ./cmd/main.go

# Run all tests
go test ./...

# Run tests for a single package
go test ./internal/group/...

# Docker (recommended for local dev with PostgreSQL)
docker-compose up --build
```

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `DATABASE_URL` | Yes | `postgres://user:password@localhost:5432/finance_manager?sslmode=disable` | Must start with `postgres://` |
| `JWT_SECRET` | Yes | — | Must be ≥ 32 characters; app refuses to start otherwise |
| `PORT` | No | `8080` | |
| `INVITE_CODE` | No | `""` | If set, signup requires this code; if empty, signup is open |
| `CORS_ORIGIN` | No | `*` | Allowed CORS origin; set to your frontend URL in production |
| `RATE_LIMIT` | No | `10` | Max requests per window |
| `RATE_WINDOW_SECONDS` | No | `60` | Rate limit window in seconds |
| `ADMIN_USERNAME` | No | `admin` | Admin dashboard login username |
| `ADMIN_PASSWORD` | Yes (for admin) | — | Admin dashboard login password; login disabled if empty |
| `GIN_MODE` | No | `debug` | Set to `release` in production to enable rate limiting, secure cookies, disable SQL runner, and skip seed data |
| `SYNC_SESSION_TTL_DAYS` | No | `90` | Days before inactive sync sessions are expired by background cleanup |
| `TOMBSTONE_RETENTION_DAYS` | No | `30` | Days to keep soft-deleted category tombstones before hard-purging; clients use tombstones to purge stale overrides |
| `REMINDER_THRESHOLD_AMOUNT` | No | `20.0` | Minimum outstanding balance (absolute value) before a settlement reminder is sent |
| `REMINDER_DAYS_OUTSTANDING` | No | `7` | Minimum days since last group activity before a settlement reminder fires |
| `LOG_LEVEL` | No | `info` | Minimum slog level: `debug`, `info`, `warn`, `error` (case-insensitive); unrecognised values fall back to `info` |

Migrations run automatically on startup via `golang-migrate`. No manual migration step needed.

## Architecture

### Package Layout

```
cmd/main.go           — Entry point; wires all routes and middleware
internal/
  auth/               — Signup/login handlers, bcrypt, JWT issuance
  budget/             — Monthly budget CRUD
  category/           — Predefined + custom categories
  config/             — Env-based config loading
  dashboard/          — Monthly analytics aggregation
  db/                 — pgx pool setup, golang-migrate runner
  db/migrations/      — SQL migration files (numbered, sequential)
  expense/            — Unified personal + group expense CRUD
  group/              — Group management, member ops, balances
  helpers/            — Shared DB utilities and decimal serialization
  middleware/         — JWT auth, CORS, rate limiter, request logger
  recurring/          — Recurring transaction definitions + scheduling (expenses & income)
  settlement/         — Settlement recording between group members
  user/               — User domain model
```

### Request Lifecycle

All requests pass through: `RequestLogger → CORS → (JWTAuth on protected routes)`. Rate limiting applies only to `/auth/*` routes (IP-based, in-memory).

`cmd/main.go` is the single source of truth for route registration. Each domain package exposes handler functions that accept a `*pgxpool.Pool` and return `gin.HandlerFunc`.

### Database

- Driver: `pgx/v5` with connection pooling (5–25 connections)
- All monetary amounts use `shopspring/decimal.Decimal` internally for precision
- Request structs accept amounts as `float64` (JSON numbers from the client), converted via `decimal.NewFromFloat()`
- Response structs use `helpers.StringDecimal` which serializes amounts as JSON numbers (unquoted)
- **All date/time fields use epoch milliseconds (int64) for client communication.** Request structs accept `int64` epoch ms; response structs use `helpers.EpochMillis` which serializes `time.Time` as unquoted int64 ms. The `date` column in `transactions` and `group_transactions` is `TIMESTAMPTZ` — full timestamps, not date-only. There is no separate `time` column.
- Parameterized queries (`$1, $2, ...`) everywhere — no string concatenation in SQL
- Expenses use soft delete (`is_deleted` flag); all queries must filter `is_deleted = FALSE`
- Balance calculations are derived at query time from `ExpenseSplit` + `Settlement` records (no materialized balance column)

### Intentional Denormalizations

- **`transactions.category`** (also `recurring_transactions.category`, `group_transactions.category`) is a free-text `VARCHAR(100)`, not FK'd. It stores a **polymorphic key string**, not a display name:
  - `<predefined_key>` — e.g. `electricity-gas`, `food-dining`. Resolved client-side against the cached predefined list and any user override row.
  - `cc-<uuid>` — a custom category owned by some user. Resolved client-side against `GET /categories`.
  - **Never** `oc-<predefined_key>` — overrides are a presentation layer; the txn always stores the underlying predefined key so the row survives override deletion.
  - No server-side validation (format or existence). The client owns rendering and rename/hide resolution.
  - Group transactions may carry `cc-<uuid>` keys owned by any group member; cross-user resolution is a client concern — the backend just returns the raw key.
- **`transactions.group_id`** exists alongside `group_transaction_id`. For group expenses, the group can be derived via `group_transactions.group_id`. However, settlement transactions have a `group_id` but no `group_transaction_id`, so the direct `group_id` column is needed for those. Both columns are nullable and serve different use cases.

### Authentication

- JWT claims carry `UserID` (UUID) and `Email`
- The `middleware/jwt.go` middleware injects the parsed claims into the Gin context
- Handlers extract the user ID via the context key set by the middleware
- **Per-user JWT-revocation timestamp**: `users.tokens_invalidated_after`. JWTAuth rejects any token whose `IssuedAt` is not strictly After this column. A small in-process cache (`middleware.JWTRevocationCache`, ~10s TTL) skips the per-request SELECT on the warm path; every code path that bumps the column must call `cache.Invalidate(userID)` to keep freshness.
- **Single active sync session per user**: a successful login invalidates every prior `sync_sessions` row for that user (`invalidated_at = now()`, `invalidation_reason = 'new_login'`) **and** bumps `tokens_invalidated_after = now() - INTERVAL '1 second'` (the 1-second backwards offset keeps the brand-new JWT alive against its own bump). Net effect: at any moment exactly one device per user can mutate.
- **Dual invalidation columns must always bump together on security events.** The two columns serve different layers — `users.tokens_invalidated_after` revokes JWTs (read-path enforcement in `JWTAuth`); `sync_sessions.invalidated_at` revokes sync sessions (write-path enforcement in `SyncSessionGuard`). Any new code path that triggers a security event (logout, password change, email change, force-revoke from admin, future password reset) must bump both, otherwise the device retains either read or write capability after the event. See `auth.invalidateJWTs` and `auth.invalidateAllSessions` for the canonical pair.

### Key Domain Rules

- **Groups**: Creating a group auto-adds the creator as the first member. Group expenses require splits summing to the expense amount.
- **Categories**: 15 predefined categories are seeded on signup via `SeedPredefinedCategories()`. Custom categories are user-scoped. Both share the `custom_categories` table (`is_predefined` flag distinguishes them).
- **Recurring transactions**: `last_added_date` tracks when the last transaction instance was generated. Supports both expense and income types. Scheduling logic lives in `recurring/`.
- **Settlements**: Recorded as `from_user → to_user` payments within a group; affect balance calculations. Personal-ledger side-effects are emitted **only for the excess portion** (`amount − max(0, pairwiseDebt)`); pure debt-clearing settlements produce no personal txns. When excess > 0 a **pair** is inserted (`expense` for `from_user` + `income` for `to_user`) linked by `settlement_id`. Inverted-direction settlements (recipient already owes payer) are allowed and book the whole amount as excess. Cross-currency settlements are rejected (`400 MIXED_CURRENCY_SETTLEMENT`). On `DeleteSettlement` the linked pair is **soft-deleted** (1-day undo); on `UpdateSettlement` (amount changed) it is **hard-deleted** and recreated..

### API Response Shape

All list endpoints return `{ "data": [...] }`. Paginated endpoints return `{ "data": [...], "pagination": {...} }`.
