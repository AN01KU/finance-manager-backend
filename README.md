# Finance Manager Backend

A Go/Gin REST API for personal finance tracking and group expense splitting, backed by PostgreSQL.

## Features

- **Authentication** — JWT signup/login with rate limiting, bcrypt passwords
- **Expenses** — Unified personal + group expense tracking, soft delete, pagination, optional client-supplied IDs
- **Groups** — Create groups, manage members, auto-derive balances from splits + settlements
- **Settlements** — Record payments between group members
- **Categories** — 15 predefined categories seeded on signup + custom user-defined categories
- **Budgets** — Monthly budgets with upsert semantics
- **Recurring Expenses** — Daily/weekly/monthly/yearly recurring expense definitions
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
| `RATE_LIMIT` | No | `10` | Max requests per window |
| `RATE_WINDOW_SECONDS` | No | `60` | Rate limit window in seconds |

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

All list endpoints return `{ "data": [...] }`. Paginated endpoints add `"pagination": { "limit", "offset", "total" }`.

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
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "username": "johndoe",
    "created_at": "2026-01-01T12:00:00Z"
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

---

### User

All protected routes require `Authorization: Bearer <token>`.

#### GET /me

Response `200`:
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "user@example.com",
  "username": "johndoe",
  "created_at": "2026-01-01T12:00:00Z"
}
```

#### DELETE /me

Deletes the authenticated user and all their data (cascade).

Response `200`: `{ "message": "user deleted successfully" }`

---

### Expenses

#### POST /expenses

Creates an expense. If `id` is provided, the request is idempotent (upsert on conflict).

```json
{
  "id": "optional-client-uuid",
  "amount": "45.50",
  "category": "foodDining",
  "date": "2026-03-15T00:00:00Z",
  "time": "2026-03-15T12:30:00Z",
  "description": "Lunch at café",
  "notes": "With colleagues",
  "recurring_expense_id": null,
  "group_id": null,
  "group_name": null
}
```

Response `201`: Expense object.

#### GET /expenses

Query params: `limit` (default 50, max 100), `offset`, `category`, `start_date` (YYYY-MM-DD), `end_date` (YYYY-MM-DD), `group_id`, `recurring_expense_id`, `is_deleted=true`

Response `200`:
```json
{
  "data": [
    {
      "id": "...",
      "user_id": "...",
      "amount": "45.50",
      "category": "foodDining",
      "date": "2026-03-15T00:00:00Z",
      "time": "2026-03-15T12:30:00Z",
      "description": "Lunch at café",
      "notes": "With colleagues",
      "is_deleted": false,
      "recurring_expense_id": null,
      "group_id": null,
      "group_name": null,
      "created_at": "...",
      "updated_at": "..."
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 142
  }
}
```

#### GET /expenses/:id

Response `200`: Single expense object.

#### PUT /expenses/:id

All fields optional (partial update):
```json
{
  "amount": "50.00",
  "category": "shopping",
  "description": "Updated description"
}
```

Response `200`: Updated expense object.

#### DELETE /expenses/:id

Soft deletes the expense (sets `is_deleted = true`).

Response `200`: `{ "message": "expense deleted successfully" }`

---

### Recurring Expenses

#### POST /recurring-expenses

Creates a recurring expense definition. If `id` is provided, uses upsert semantics.

```json
{
  "id": "optional-client-uuid",
  "name": "Netflix",
  "amount": "15.99",
  "category": "entertainment",
  "frequency": "monthly",
  "day_of_month": 1,
  "start_date": "2026-01-01T00:00:00Z",
  "end_date": null,
  "notes": "Streaming subscription"
}
```

`frequency` must be one of: `daily`, `weekly`, `monthly`, `yearly`
- `weekly` requires `days_of_week` (array of 0–6, 0=Sunday)
- `monthly` requires `day_of_month` (1–31)

Response `201`: RecurringExpense object.

#### GET /recurring-expenses

Query params: `active=true` (filter to active only)

Response `200`: `{ "data": [...] }`

#### GET /recurring-expenses/:id

Response `200`: Single RecurringExpense object.

#### PUT /recurring-expenses/:id

All fields optional. Changing frequency requires relevant fields (`day_of_month` or `days_of_week`).

Response `200`: Updated RecurringExpense object.

#### DELETE /recurring-expenses/:id

Response `200`: `{ "message": "recurring expense deleted successfully" }`

---

### Budgets

#### POST /budgets

Creates or updates the budget for a given month/year. If `id` is provided, the request is idempotent.

```json
{
  "id": "optional-client-uuid",
  "limit": "3000.00",
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
      "limit": "3000.00",
      "month": 3,
      "year": 2026,
      "created_at": "...",
      "updated_at": "..."
    }
  ]
}
```

#### PUT /budgets/:id

All fields optional:
```json
{
  "limit": "3500.00"
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
  "name": "Groceries",
  "icon": "cart.fill",
  "color": "#4CAF50"
}
```

`color` must be a 7-character hex string (e.g. `#4CAF50`).
`icon` uses SF Symbol names.

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
      "created_at": "...",
      "updated_at": "..."
    }
  ]
}
```

Predefined categories are returned first, then custom categories alphabetically.

#### PUT /categories/:id

All fields optional:
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
      "category": "foodDining",
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
  "created_at": "..."
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
      "created_at": "...",
      "members": [
        { "id": "...", "email": "user@example.com", "username": "johndoe", "createdAt": "..." }
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
    "created_at": "...",
    "members": [...],
    "balances": [...],
    "expenses": [
      {
        "id": "...",
        "description": "Hotel",
        "total_amount": "5000.00",
        "paid_by": "...",
        "created_at": "..."
      }
    ]
  },
  "is_member": true
}
```

#### POST /groups/:id/add-member

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
    { "id": "...", "email": "user@example.com", "username": "johndoe", "createdAt": "..." }
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

---

### Settlements

#### POST /settlements

Records a payment from one group member to another.

```json
{
  "group_id": "...",
  "from_user": "...",
  "to_user": "...",
  "amount": "250.00"
}
```

All three users (requester, `from_user`, `to_user`) must be group members. `from_user` and `to_user` must differ.

Response `201`:
```json
{
  "id": "...",
  "group_id": "...",
  "from_user": "...",
  "to_user": "...",
  "amount": "250.00",
  "created_at": "..."
}
```

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

### recurring_expenses
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `name` | VARCHAR(255) | |
| `amount` | DECIMAL(12,2) | > 0 |
| `category` | VARCHAR(100) | |
| `frequency` | VARCHAR(20) | `daily/weekly/monthly/yearly` |
| `day_of_month` | INTEGER | 1–31, used for monthly |
| `days_of_week` | INTEGER[] | 0–6, used for weekly |
| `start_date` | TIMESTAMPTZ | |
| `end_date` | TIMESTAMPTZ | Optional |
| `is_active` | BOOLEAN | Default true |
| `last_added_date` | TIMESTAMPTZ | Tracks last auto-generated expense |
| `notes` | TEXT | Optional |
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### expenses
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `user_id` | UUID | FK → users |
| `amount` | DECIMAL(12,2) | > 0 |
| `category` | VARCHAR(100) | |
| `date` | TIMESTAMPTZ | |
| `time` | TIMESTAMPTZ | Optional |
| `description` | VARCHAR(255) | Optional |
| `notes` | TEXT | Optional |
| `is_deleted` | BOOLEAN | Soft delete |
| `recurring_expense_id` | UUID | FK → recurring_expenses, optional |
| `group_id` | UUID | Optional, for group expenses |
| `group_name` | VARCHAR(255) | Optional |
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
| `created_at` | TIMESTAMPTZ | |
| `updated_at` | TIMESTAMPTZ | |

### group_members
| Column | Type | Notes |
|--------|------|-------|
| `group_id` | UUID | FK → groups |
| `user_id` | UUID | FK → users |
| `joined_at` | TIMESTAMPTZ | |

Primary key: `(group_id, user_id)`

### expense_splits
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `expense_id` | UUID | FK → expenses |
| `user_id` | UUID | FK → users |
| `amount` | DECIMAL(12,2) | ≥ 0 |

Unique constraint: `(expense_id, user_id)`

### settlements
| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID | Primary key |
| `group_id` | UUID | FK → groups |
| `from_user` | UUID | FK → users |
| `to_user` | UUID | FK → users |
| `amount` | DECIMAL(12,2) | > 0 |
| `created_at` | TIMESTAMPTZ | |

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
  main.go                  Entry point; wires all routes and middleware
internal/
  auth/                    Signup/login handlers, bcrypt, JWT issuance
  budget/                  Monthly budget CRUD
  category/                Predefined + custom categories
  config/                  Env-based config loading
  dashboard/               Monthly analytics aggregation
  db/                      pgx pool setup, golang-migrate runner
  db/migrations/           SQL migration files
  expense/                 Unified personal + group expense CRUD
  group/                   Group management, member ops, balances
  helpers/                 Shared DB utilities and decimal serialization
  middleware/              JWT auth, CORS, rate limiter, request logger
  recurring/               Recurring expense definitions + scheduling
  settlement/              Settlement recording between group members
  seed/                    Test data seeding with auto-cleanup on shutdown
  user/                    User domain model
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
