# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
| `RATE_LIMIT` | No | `10` | Max requests per window |
| `RATE_WINDOW_SECONDS` | No | `60` | Rate limit window in seconds |
| `GIN_MODE` | No | `debug` | Set to `release` in production to enable rate limiting (rate limiter is skipped in non-release mode) |

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
- All monetary amounts use `shopspring/decimal.Decimal` (never `float64`)
- Custom `helpers.StringDecimal` serializes Decimal as a JSON string to avoid precision loss
- Parameterized queries (`$1, $2, ...`) everywhere — no string concatenation in SQL
- Expenses use soft delete (`is_deleted` flag); all queries must filter `is_deleted = FALSE`
- Balance calculations are derived at query time from `ExpenseSplit` + `Settlement` records (no materialized balance column)

### Authentication

- JWT claims carry `UserID` (UUID) and `Email`
- The `middleware/jwt.go` middleware injects the parsed claims into the Gin context
- Handlers extract the user ID via the context key set by the middleware

### Key Domain Rules

- **Groups**: Creating a group auto-adds the creator as the first member. Group expenses require splits summing to the expense amount.
- **Categories**: 15 predefined categories are seeded on signup via `SeedPredefinedCategories()`. Custom categories are user-scoped. Both share the `custom_categories` table (`is_predefined` flag distinguishes them).
- **Recurring transactions**: `last_added_date` tracks when the last transaction instance was generated. Supports both expense and income types. Scheduling logic lives in `recurring/`.
- **Settlements**: Recorded as `from_user → to_user` payments within a group; affect balance calculations. Creating a settlement also inserts an income transaction for the `to_user`.

### API Response Shape

All list endpoints return `{ "data": [...] }`. Paginated endpoints return `{ "data": [...], "pagination": {...} }`.
