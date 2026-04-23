# Finance Manager Backend

A Go/Gin REST API for personal finance tracking and group expense splitting, backed by PostgreSQL.

## Features

- **Authentication** — JWT signup/login with rate limiting, bcrypt passwords
- **Transactions** — Unified expense + income tracking, soft delete, pagination, optional client-supplied IDs
- **Groups** — Create groups, manage members, auto-derive balances from splits + settlements
- **Settlements** — Record payments between group members; creates an income transaction for the recipient
- **Categories** — 15 predefined categories seeded on signup + custom user-defined categories
- **Budgets** — Monthly budgets with upsert semantics
- **Recurring Transactions** — Daily/weekly/monthly/yearly recurring expense and income definitions
- **Dashboard** — Monthly analytics with daily averages, projections, and category breakdown

## Prerequisites

- Go 1.24.2+
- Docker and Docker Compose (recommended)

## Setup

### Docker (recommended)

```bash
docker-compose up --build
```

Migrations run automatically on startup. Server available at `http://localhost:8080`.

### Local Development

**macOS:**
```bash
brew install postgresql@15
brew services start postgresql@15
createdb finance_manager
```

**Linux:**
```bash
sudo apt-get install postgresql postgresql-contrib
sudo systemctl start postgresql
sudo -u postgres createdb finance_manager
```

Set environment variables:
```bash
export DATABASE_URL="postgres://postgres@localhost/finance_manager?sslmode=disable"
export JWT_SECRET="your-secret-must-be-at-least-32-characters-long"
export PORT="8080"
```

Run:
```bash
go run ./cmd/main.go
```

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| `DATABASE_URL` | Yes | `postgres://user:password@localhost:5432/finance_manager?sslmode=disable` | Must start with `postgres://` |
| `JWT_SECRET` | Yes | — | Must be ≥ 32 characters |
| `PORT` | No | `8080` | |
| `INVITE_CODE` | No | *(empty)* | If set, signup requires this code |
| `CORS_ORIGIN` | No | `*` | Set to frontend URL in production |
| `RATE_LIMIT` | No | `10` | Max requests per window |
| `RATE_WINDOW_SECONDS` | No | `60` | Rate limit window in seconds |
| `ADMIN_USERNAME` | No | `admin` | |
| `ADMIN_PASSWORD` | Yes (for admin) | — | Admin login disabled if empty |
| `GIN_MODE` | No | `debug` | Set to `release` in production |
| `SYNC_SESSION_TTL_DAYS` | No | `90` | |

## Development

```bash
# Hot reload (requires: go install github.com/cosmtrek/air@latest)
air

# Build
go build -o finance-manager ./cmd/main.go

# Tests
go test ./...
```

---

## API Reference

All monetary amounts are returned as JSON strings (e.g. `"123.45"`) to avoid float precision loss.

All dates and timestamps are **epoch milliseconds** (integers), both in requests and responses.

All list endpoints return `{ "data": [...] }`. Paginated endpoints add `"pagination": { "limit", "offset", "total" }`.

All protected routes require `Authorization: Bearer <token>`.

### Authentication

All auth endpoints are rate-limited (10 req/60s per IP).

#### POST /auth/signup

```json
{
  "email": "user@example.com",
  "username": "johndoe",
  "password": "securepassword"
}
```

Response `201`:
```json
{
  "token": "eyJhbGci...",
  "sync_session_id": "a1b2c3d4-...",
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "username": "johndoe",
    "created_at": 1748736000000
  }
}
```

Automatically seeds 15 predefined categories for the new user.

#### POST /auth/login

```json
{
  "email": "user@example.com",
  "password": "securepassword"
}
```

Response `200`: Same shape as signup.

#### POST /auth/logout

Invalidates sync session(s). If `sync_session_id` is provided, only that session is invalidated. Otherwise, all active sessions are invalidated.

```json
{ "sync_session_id": "optional-uuid" }
```

Response `200`: `{ "message": "logged out successfully" }`

---

### User

#### GET /me

Response `200`:
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "user@example.com",
  "username": "johndoe",
  "created_at": 1748736000000
}
```

#### PATCH /me

All fields optional (partial update).

```json
{
  "username": "newname",
  "email": "new@example.com",
  "password": "newpassword"
}
```

Response `200`: Updated user object.

#### DELETE /me

Deletes the authenticated user and all their data (cascade).

Response `200`: `{ "message": "user deleted successfully" }`

---

### Transactions

#### POST /transactions

Creates a personal transaction (expense or income). If `id` is provided, the request is idempotent (upsert on conflict).

```json
{
  "id": "optional-client-uuid",
  "type": "expense",
  "amount": "350",
  "category": "Food & Dining",
  "date": 1774569600000,
  "time": 1774583400000,
  "description": "Lunch at office",
  "notes": "With team",
  "recurring_transaction_id": null
}
```

`type` must be `expense` or `income`.

Response `201`: Transaction object.

#### GET /transactions

Query params: `type` (`expense`/`income`), `category`, `start_date` (epoch ms), `end_date` (epoch ms), `group_transaction_id`, `recurring_transaction_id`, `is_deleted=true`, `limit` (default 50, max 100), `offset`

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "user_id": "...",
      "type": "expense",
      "amount": "350",
      "category": "Food & Dining",
      "date": 1774569600000,
      "description": "Lunch at office",
      "group_transaction_id": null,
      "group_id": null,
      "group_name": null,
      "settlement_id": null,
      "recurring_transaction_id": null,
      "is_deleted": false,
      "created_at": 1774569600000,
      "updated_at": 1774569600000
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 142
  }
}
```

#### GET /transactions/:id

Response `200`: Single transaction object.

#### PATCH /transactions/:id

All fields optional (partial update). Cannot update group transactions via this endpoint — use `/groups/:id/transactions/:txId` instead.

```json
{
  "type": "expense",
  "amount": "400",
  "category": "Food & Dining",
  "date": 1774569600000,
  "description": "Updated description",
  "notes": "Some note"
}
```

Response `200`: Updated transaction object.

#### DELETE /transactions/:id

Soft deletes the transaction (sets `is_deleted = true`). Cannot delete group transactions via this endpoint.

Response `200`: `{ "message": "transaction deleted successfully" }`

---

### Recurring Transactions

#### POST /recurring-transactions

Creates a recurring transaction definition. If `id` is provided, uses upsert semantics.

```json
{
  "id": "optional-client-uuid",
  "type": "expense",
  "name": "House Rent",
  "amount": "15000",
  "category": "Housing",
  "frequency": "monthly",
  "day_of_month": 1,
  "start_date": 1767225600000,
  "end_date": null,
  "notes": "Monthly rent"
}
```

`type` must be `expense` or `income`.
`frequency` must be one of: `daily`, `weekly`, `monthly`, `yearly`.
- `weekly` requires `days_of_week` (array of 0–6, 0=Sunday)
- `monthly` requires `day_of_month` (1–31)

Response `201`: RecurringTransaction object.

#### GET /recurring-transactions

Query params: `active=true` (filter to active only)

Response `200`: `{ "data": [...] }`

#### GET /recurring-transactions/:id

Response `200`: Single RecurringTransaction object.

#### PATCH /recurring-transactions/:id

All fields optional. Changing frequency requires the relevant fields (`day_of_month` or `days_of_week`).

```json
{
  "amount": "16000",
  "is_active": true
}
```

Response `200`: Updated RecurringTransaction object.

#### DELETE /recurring-transactions/:id

Response `200`: `{ "message": "recurring transaction deleted successfully" }`

---

### Budgets

#### POST /budgets

Creates or updates the budget for a given month/year. If `id` is provided, the request is idempotent.

```json
{
  "id": "optional-client-uuid",
  "limit": "30000",
  "month": 3,
  "year": 2026
}
```

Response `200`: Budget object.

#### GET /budgets

Query params: `month` (1–12), `year` (2000–2100)

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "user_id": "...",
      "limit": "30000",
      "month": 3,
      "year": 2026,
      "created_at": 1748736000000,
      "updated_at": 1748736000000
    }
  ]
}
```

#### PATCH /budgets/:id

```json
{
  "limit": "35000"
}
```

Response `200`: Updated budget object.

#### DELETE /budgets/:id

Response `200`: `{ "message": "budget deleted successfully" }`

---

### Categories

15 predefined categories are seeded on signup. Custom categories can be added by the user.

#### POST /categories

Creates a custom category. If `id` is provided, uses upsert semantics.

```json
{
  "id": "optional-client-uuid",
  "name": "Investments",
  "icon": "chart.line.uptrend.xyaxis.circle.fill",
  "color": "#27AE60"
}
```

`color` must be a 7-character hex string (e.g. `#27AE60`). `icon` uses SF Symbol names.

Response `201`: Category object.

#### GET /categories

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "user_id": "...",
      "name": "Food & Dining",
      "icon": "fork.knife.circle.fill",
      "color": "#FF6B6B",
      "is_hidden": false,
      "is_predefined": true,
      "predefined_key": "foodDining",
      "created_at": 1748736000000,
      "updated_at": 1748736000000
    }
  ]
}
```

Predefined categories are returned first, then custom categories alphabetically.

#### PATCH /categories/:id

```json
{
  "name": "Food & Groceries",
  "color": "#66BB6A",
  "icon": "fork.knife.circle.fill",
  "is_hidden": false
}
```

Response `200`: Updated category object.

#### DELETE /categories/:id

The `other` predefined category cannot be deleted.

Response `200`: `{ "message": "category deleted successfully" }`

---

### Predefined Categories

| predefined_key | name | icon | color |
|---|---|---|---|
| `foodDining` | Food & Dining | `fork.knife.circle.fill` | `#FF6B6B` |
| `transport` | Transport | `car.circle.fill` | `#4ECDC4` |
| `housing` | Housing | `house.circle.fill` | `#45B7D1` |
| `healthMedical` | Health & Medical | `cross.case.circle.fill` | `#96CEB4` |
| `shopping` | Shopping | `bag.circle.fill` | `#FFEAA7` |
| `utilities` | Utilities | `bolt.square.fill` | `#DDA15E` |
| `entertainment` | Entertainment | `gamecontroller.circle.fill` | `#BC6C25` |
| `travel` | Travel | `airplane.circle.fill` | `#8E44AD` |
| `workProfessional` | Work & Professional | `briefcase.circle.fill` | `#34495E` |
| `education` | Education | `book.circle.fill` | `#3498DB` |
| `debtPayments` | Debt & Payments | `creditcard.circle.fill` | `#2C3E50` |
| `booksMedia` | Books & Media | `book.closed.circle.fill` | `#E74C3C` |
| `familyKids` | Family & Kids | `figure.2.and.child.holdinghands` | `#F39C12` |
| `gifts` | Gifts | `gift.circle.fill` | `#E91E63` |
| `other` | Other | `ellipsis.circle.fill` | `#95A5A6` |

---

### Dashboard

#### GET /dashboard/monthly

Query params: `month` (1–12), `year` (2000–2100) — defaults to current month.

Response `200`:
```json
{
  "month": 3,
  "year": 2026,
  "total_expenses": "1250.75",
  "expense_count": 45,
  "budget": "3000.00",
  "remaining_budget": "1749.25",
  "days_in_month": 31,
  "days_elapsed": 15,
  "days_remaining": 16,
  "daily_average_spent": "83.38",
  "projected_spending": "2584.78",
  "is_over_budget": false,
  "category_breakdown": [
    {
      "category": "Food & Dining",
      "total_amount": "450.50",
      "expense_count": 12
    }
  ]
}
```

`budget`, `remaining_budget`, and `projected_spending` are omitted if no budget is set for the month.

---

### Groups

#### POST /groups

```json
{
  "name": "Goa Trip"
}
```

Creator is automatically added as the first member.

Response `201`:
```json
{
  "id": "...",
  "name": "Goa Trip",
  "created_by": "...",
  "created_at": 1748736000000
}
```

#### GET /groups

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "name": "Goa Trip",
      "created_by": "...",
      "created_at": 1748736000000,
      "members": [
        { "id": "...", "email": "user@example.com", "username": "johndoe", "createdAt": 1748736000000 }
      ],
      "balances": [
        { "user_id": "...", "amount": "250.00" }
      ]
    }
  ]
}
```

#### GET /groups/:id

Response `200`:
```json
{
  "group": {
    "id": "...",
    "name": "Goa Trip",
    "created_by": "...",
    "created_at": 1748736000000,
    "members": [...],
    "balances": [...],
    "expenses": [...],
    "settlements": [...]
  },
  "is_member": true
}
```

#### POST /groups/:id/members

```json
{
  "email": "member@example.com"
}
```

Response `200`: `{ "message": "member added" }`

#### GET /groups/:id/members

Response `200`:
```json
{
  "members": [
    { "id": "...", "email": "user@example.com", "username": "johndoe", "createdAt": 1748736000000 }
  ]
}
```

#### GET /groups/:id/balances

Balance semantics: positive = user is owed money; negative = user owes money.

Response `200`:
```json
[
  { "user_id": "...", "amount": "250.00" },
  { "user_id": "...", "amount": "-250.00" }
]
```

#### GET /groups/:id/settlements

Query params: `limit` (default 20), `offset`

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "group_id": "...",
      "from_user": "...",
      "to_user": "...",
      "amount": "500.00",
      "notes": "Partial hotel payment",
      "created_at": 1748736000000
    }
  ],
  "pagination": {
    "limit": 20,
    "offset": 0,
    "total": 5
  }
}
```

---

### Group Transactions

#### POST /groups/:id/transactions

Creates a group expense with per-member splits. Splits must sum to `total_amount`.

```json
{
  "paid_by_user_id": "...",
  "total_amount": "1200",
  "category": "Food & Dining",
  "date": 1774569600000,
  "description": "Dinner split 3 ways",
  "splits": [
    { "user_id": "...", "amount": "400" },
    { "user_id": "...", "amount": "400" },
    { "user_id": "...", "amount": "400" }
  ]
}
```

Response `201`: GroupTransaction object with splits.

#### GET /groups/:id/transactions

Response `200`: `{ "data": [...] }`

#### GET /groups/:id/transactions/:txId

Response `200`: Single GroupTransaction object with splits.

#### PATCH /groups/:id/transactions/:txId

All fields optional:
```json
{
  "description": "Updated description",
  "notes": "Some note"
}
```

Response `200`: Updated GroupTransaction object.

#### DELETE /groups/:id/transactions/:txId

Soft deletes the group transaction and its associated personal transaction records.

Response `200`: `{ "message": "group transaction deleted" }`

---

### Settlements

#### POST /settlements

Records a payment from one group member to another. Also creates an income transaction for `to_user` in the `Debt & Payments` category.

```json
{
  "group_id": "...",
  "from_user": "...",
  "to_user": "...",
  "amount": "250.00",
  "notes": "Settling electricity + wifi"
}
```

All of the requester, `from_user`, and `to_user` must be group members. `from_user` and `to_user` must differ.

Response `201`:
```json
{
  "id": "...",
  "group_id": "...",
  "from_user": "...",
  "to_user": "...",
  "amount": "250.00",
  "notes": "Settling electricity + wifi",
  "created_at": 1748736000000
}
```

#### GET /settlements/:id

Returns a single settlement. Requires group membership.

Response `200`: Settlement object.

---

### Health

#### GET /health

Response `200`:
```json
{
  "status": "healthy",
  "database": "connected"
}
```

---

## Database Schema

### users
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key, auto-generated |
| `email` | VARCHAR(255) | Unique |
| `username` | VARCHAR(100) | |
| `password_hash` | VARCHAR(255) | bcrypt |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### recurring_transactions
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `type` | VARCHAR(10) | `expense` or `income` |
| `name` | VARCHAR(255) | |
| `amount` | DECIMAL(12,2) | > 0 |
| `category` | VARCHAR(100) | |
| `frequency` | VARCHAR(20) | `daily/weekly/monthly/yearly` |
| `day_of_month` | INTEGER | 1–31, used for monthly |
| `days_of_week` | INTEGER[] | 0–6, used for weekly |
| `start_date` | TIMESTAMPTZ | |
| `end_date` | TIMESTAMPTZ | Optional |
| `is_active` | BOOLEAN | Default true |
| `last_added_date` | TIMESTAMPTZ | Tracks last auto-generated transaction |
| `notes` | TEXT | Optional |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### transactions
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `type` | VARCHAR(10) | `expense` or `income` |
| `amount` | DECIMAL(12,2) | > 0 |
| `category` | VARCHAR(100) | |
| `date` | TIMESTAMPTZ | |
| `description` | VARCHAR(255) | Optional |
| `notes` | TEXT | Optional |
| `recurring_transaction_id` | UUID | FK → recurring_transactions, optional |
| `group_transaction_id` | UUID | FK → group_transactions, optional |
| `group_id` | UUID | FK → groups, optional |
| `settlement_id` | UUID | FK → settlements, optional |
| `is_deleted` | BOOLEAN | Soft delete |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### monthly_budgets
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `year` | INTEGER | 2000–2100 |
| `month` | INTEGER | 1–12 |
| `budget_limit` | DECIMAL(12,2) | ≥ 0; exposed as `limit` in API |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

Unique constraint: `(user_id, year, month)`

### custom_categories
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `name` | VARCHAR(100) | |
| `icon` | VARCHAR(100) | SF Symbol name |
| `color` | VARCHAR(7) | Hex color e.g. `#FF6B6B` |
| `is_hidden` | BOOLEAN | |
| `is_predefined` | BOOLEAN | |
| `predefined_key` | VARCHAR(50) | Optional; unique per user when set |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

Unique constraints: `(user_id, name)`, `(user_id, predefined_key) WHERE predefined_key IS NOT NULL`

### groups
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `name` | VARCHAR(255) | |
| `created_by` | UUID | FK → users |
| `is_deleted` | BOOLEAN | Soft delete |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### group_members
| Column | Type | Notes |
|--------|------|-------|
| `group_id` | UUID | FK → groups |
| `user_id` | UUID | FK → users |
| `joined_at` | TIMESTAMPTZ | |

Primary key: `(group_id, user_id)`

### group_transactions
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `group_id` | UUID | FK → groups |
| `paid_by_user_id` | UUID | FK → users |
| `total_amount` | DECIMAL(12,2) | > 0 |
| `category` | VARCHAR(100) | |
| `date` | TIMESTAMPTZ | |
| `description` | VARCHAR(255) | Optional |
| `notes` | TEXT | Optional |
| `is_deleted` | BOOLEAN | Soft delete |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### group_transaction_splits
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `group_transaction_id` | UUID | FK → group_transactions |
| `user_id` | UUID | FK → users |
| `amount` | DECIMAL(12,2) | ≥ 0 |
| `transaction_id` | UUID | FK → transactions, optional |

Unique constraint: `(group_transaction_id, user_id)`

### settlements
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `group_id` | UUID | FK → groups |
| `from_user` | UUID | FK → users |
| `to_user` | UUID | FK → users |
| `amount` | DECIMAL(12,2) | > 0 |
| `notes` | TEXT | Optional |
| `is_deleted` | BOOLEAN | Soft delete |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

Constraint: `from_user != to_user`

---

## Postman

Import `Finance_Manager_Backend.postman_collection.json`. The collection includes:

- Pre-configured requests for all API endpoints
- Collection variables: `base_url` (default `http://localhost:8080`) and `token`
- Automatic token extraction after signup/login
- Example request bodies for all endpoints

**Quick start:**
1. Import the collection
2. Run **Signup** or **Login** — token is saved automatically
3. All authenticated requests use `{{token}}` from the collection variable

---

## Project Structure

```
cmd/
  main.go                    Entry point; wires all routes and middleware
internal/
  auth/                      Signup/login handlers, bcrypt, JWT issuance
  budget/                    Monthly budget CRUD
  category/                  Predefined + custom categories
  config/                    Env-based config loading
  dashboard/                 Monthly analytics aggregation
  db/                        pgx pool setup, golang-migrate runner
  db/migrations/             SQL migration files
  group/                     Group management, member ops, balances, group transactions
  helpers/                   Shared DB utilities and decimal serialization
  middleware/                JWT auth, CORS, rate limiter, request logger
  recurring/                 Recurring transaction definitions + scheduling
  settlement/                Settlement recording between group members
  seed/                      Test data seeding with auto-cleanup on shutdown
  transaction/               Personal transaction CRUD (expenses + income)
  user/                      User domain model
```

## Troubleshooting

**Database connection error:**
- Ensure PostgreSQL is running
- Check `DATABASE_URL` is correct
- Verify database exists: `createdb finance_manager`

**Migration error (permission denied):**
- Check file permissions: `chmod 644 internal/db/migrations/*.sql`

**Invalid token:**
- Pass token in `Authorization: Bearer <token>` header
- Tokens expire after 24 hours
- Ensure `JWT_SECRET` is the same value used at signup

## License

MIT
