# Backend TODO — Full Rewrite for iOS Sync + Groups (Splitwise)

> **Date:** 2026-03-20
> **Context:** The iOS app (money-manager-ios) has evolved significantly. The backend models are outdated and need a fresh schema. We do NOT care about old data — drop everything and recreate.
> **Branch:** `feat/server-sync` (iOS side)

---

## 🏗️ Phase 0: Fresh Database Schema

**Drop all existing tables and migrations. Start clean.**

### New Tables (Postgres DDL)

#### `users`
No changes needed — keep as-is.
```sql
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    username VARCHAR(100) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

#### `expenses` (was `personal_expenses`)
Mapped from iOS `Expense` model. Major changes:
- Renamed from `personal_expenses` → `expenses`
- Added `time` field (optional, separate from date)
- Added `is_deleted` soft-delete flag (iOS uses this)
- Added `group_id` + `group_name` for Splitwise feature (nullable, unused by client for now)
- Added `recurring_expense_id` FK linking to the recurring expense that generated it
- Removed `is_recurring`, `frequency`, `day_of_month`, `days_of_week`, `recurring_end_date`, `is_active` — these now live on `recurring_expenses` table

```sql
CREATE TABLE expenses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    category VARCHAR(100) NOT NULL,
    date TIMESTAMPTZ NOT NULL,
    time TIMESTAMPTZ,
    description VARCHAR(255),
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    is_deleted BOOLEAN DEFAULT FALSE,
    recurring_expense_id UUID REFERENCES recurring_expenses(id) ON DELETE SET NULL,
    group_id UUID,       -- for future Splitwise feature
    group_name VARCHAR(255) -- for future Splitwise feature
);

CREATE INDEX idx_expenses_user_id ON expenses(user_id);
CREATE INDEX idx_expenses_date ON expenses(user_id, date DESC);
CREATE INDEX idx_expenses_category ON expenses(user_id, category);
CREATE INDEX idx_expenses_recurring ON expenses(recurring_expense_id);
CREATE INDEX idx_expenses_group ON expenses(group_id);
CREATE INDEX idx_expenses_not_deleted ON expenses(user_id, is_deleted) WHERE is_deleted = FALSE;
```

#### `recurring_expenses` (NEW — was embedded fields in `personal_expenses`)
Mapped from iOS `RecurringExpense` model. This is now a **standalone table**.

```sql
CREATE TABLE recurring_expenses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    category VARCHAR(100) NOT NULL,
    frequency VARCHAR(20) NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    day_of_month INTEGER CHECK (day_of_month >= 1 AND day_of_month <= 31),
    days_of_week INTEGER[],  -- 0=Sun, 1=Mon, ..., 6=Sat
    start_date TIMESTAMPTZ NOT NULL,
    end_date TIMESTAMPTZ,
    is_active BOOLEAN DEFAULT TRUE,
    last_added_date TIMESTAMPTZ,
    notes TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_recurring_expenses_user_id ON recurring_expenses(user_id);
CREATE INDEX idx_recurring_expenses_active ON recurring_expenses(user_id, is_active) WHERE is_active = TRUE;
```

#### `monthly_budgets`
Mapped from iOS `MonthlyBudget` model. Renamed `amount` → `budget_limit` to match iOS `limit` field.

```sql
CREATE TABLE monthly_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year INTEGER NOT NULL CHECK (year >= 2000 AND year <= 2100),
    month INTEGER NOT NULL CHECK (month >= 1 AND month <= 12),
    budget_limit DECIMAL(12,2) NOT NULL CHECK (budget_limit >= 0),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, year, month)
);

CREATE INDEX idx_monthly_budgets_user ON monthly_budgets(user_id, year, month);
```

#### `custom_categories` (was `expense_categories`)
Mapped from iOS `CustomCategory` model. Major changes:
- Renamed from `expense_categories` → `custom_categories`
- Added `is_hidden` flag
- Added `is_predefined` flag + `predefined_key` (for mapping to the 15 built-in categories)
- Added `updated_at`

```sql
CREATE TABLE custom_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    icon VARCHAR(100) NOT NULL,
    color VARCHAR(7) NOT NULL,     -- hex color e.g. #FF6B6B
    is_hidden BOOLEAN DEFAULT FALSE,
    is_predefined BOOLEAN DEFAULT FALSE,
    predefined_key VARCHAR(50),    -- e.g. "foodDining", "transport", etc.
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, name)
);

CREATE INDEX idx_custom_categories_user_id ON custom_categories(user_id);
```

**Predefined category keys for seeding:**
| Key | Display Name | Icon | Color |
|-----|-------------|------|-------|
| foodDining | Food & Dining | fork.knife.circle.fill | #FF6B6B |
| transport | Transport | car.circle.fill | #4ECDC4 |
| housing | Housing | house.circle.fill | #45B7D1 |
| healthMedical | Health & Medical | cross.case.circle.fill | #96CEB4 |
| shopping | Shopping | bag.circle.fill | #FFEAA7 |
| utilities | Utilities | bolt.square.fill | #DDA15E |
| entertainment | Entertainment | gamecontroller.circle.fill | #BC6C25 |
| travel | Travel | airplane.circle.fill | #8E44AD |
| workProfessional | Work & Professional | briefcase.circle.fill | #34495E |
| education | Education | book.circle.fill | #3498DB |
| debtPayments | Debt & Payments | creditcard.circle.fill | #2C3E50 |
| booksMedia | Books & Media | book.closed.circle.fill | #E74C3C |
| familyKids | Family & Kids | figure.2.and.child.holdinghands | #F39C12 |
| gifts | Gifts | gift.circle.fill | #E91E63 |
| other | Other | ellipsis.circle.fill | #95A5A6 |

---

## 🔵 Phase 1: Core Personal Finance APIs (Priority: P0)

These APIs must match what the iOS app currently does locally, so the app can sync.

### 1.1 Expenses CRUD

**Package:** `internal/expense/` (replace both old `personalexpense/` and `expense/`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/expenses` | Create expense |
| GET | `/expenses` | List expenses (paginated, filterable) |
| GET | `/expenses/:id` | Get single expense |
| PUT | `/expenses/:id` | Update expense |
| DELETE | `/expenses/:id` | Soft-delete (set `is_deleted = true`) |

**Request/Response structs must match iOS model:**
```go
type Expense struct {
    ID                 uuid.UUID  `json:"id"`
    UserID             uuid.UUID  `json:"user_id"`
    Amount             string     `json:"amount"`      // StringDecimal
    Category           string     `json:"category"`
    Date               time.Time  `json:"date"`
    Time               *time.Time `json:"time,omitempty"`
    Description        *string    `json:"description,omitempty"`
    Notes              *string    `json:"notes,omitempty"`
    CreatedAt          time.Time  `json:"created_at"`
    UpdatedAt          time.Time  `json:"updated_at"`
    IsDeleted          bool       `json:"is_deleted"`
    RecurringExpenseID *uuid.UUID `json:"recurring_expense_id,omitempty"`
    GroupID            *uuid.UUID `json:"group_id,omitempty"`
    GroupName          *string    `json:"group_name,omitempty"`
}
```

**Filters for `GET /expenses`:**
- `?category=Food+%26+Dining`
- `?start_date=2026-01-01&end_date=2026-01-31`
- `?is_deleted=false` (default: false)
- `?group_id=uuid` (for future)
- `?recurring_expense_id=uuid`
- Pagination: `?limit=50&offset=0`

**DELETE behavior:** Soft delete — set `is_deleted = true`, `updated_at = NOW()`. Do NOT hard delete.

### 1.2 Recurring Expenses CRUD

**Package:** `internal/recurring/` (NEW)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/recurring-expenses` | Create recurring expense |
| GET | `/recurring-expenses` | List all (filterable by `?active=true`) |
| GET | `/recurring-expenses/:id` | Get single |
| PUT | `/recurring-expenses/:id` | Update |
| DELETE | `/recurring-expenses/:id` | Hard delete (+ delete generated expenses?) |

**Struct:**
```go
type RecurringExpense struct {
    ID            uuid.UUID  `json:"id"`
    UserID        uuid.UUID  `json:"user_id"`
    Name          string     `json:"name"`
    Amount        string     `json:"amount"`    // StringDecimal
    Category      string     `json:"category"`
    Frequency     string     `json:"frequency"` // daily|weekly|monthly|yearly
    DayOfMonth    *int       `json:"day_of_month,omitempty"`
    DaysOfWeek    []int      `json:"days_of_week,omitempty"`
    StartDate     time.Time  `json:"start_date"`
    EndDate       *time.Time `json:"end_date,omitempty"`
    IsActive      bool       `json:"is_active"`
    LastAddedDate *time.Time `json:"last_added_date,omitempty"`
    Notes         *string    `json:"notes,omitempty"`
    CreatedAt     time.Time  `json:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at"`
}
```

**Validation rules:**
- If `frequency == "weekly"` → `days_of_week` must be non-empty, values 0-6
- If `frequency == "monthly"` → `day_of_month` must be 1-31
- `start_date` is required
- `end_date` must be after `start_date` if provided

### 1.3 Monthly Budgets CRUD

**Package:** `internal/budget/` (update existing)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/budgets` | Create/upsert budget for a month |
| GET | `/budgets` | List all budgets |
| GET | `/budgets?month=3&year=2026` | Get specific month |
| PUT | `/budgets/:id` | Update budget |
| DELETE | `/budgets/:id` | Delete budget |

**Struct:**
```go
type MonthlyBudget struct {
    ID        uuid.UUID `json:"id"`
    UserID    uuid.UUID `json:"user_id"`
    Year      int       `json:"year"`
    Month     int       `json:"month"`
    Limit     string    `json:"limit"`  // StringDecimal — maps to iOS "limit" field
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

**Note:** iOS calls this field `limit`, backend column is `budget_limit` (reserved word). JSON key must be `"limit"`.

### 1.4 Custom Categories CRUD

**Package:** `internal/category/` (update existing)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/categories` | Create custom category |
| GET | `/categories` | List all (include predefined + custom) |
| PUT | `/categories/:id` | Update (name, icon, color, is_hidden) |
| DELETE | `/categories/:id` | Delete (block if `predefined_key == "other"`) |

**Struct:**
```go
type CustomCategory struct {
    ID            uuid.UUID `json:"id"`
    UserID        uuid.UUID `json:"user_id"`
    Name          string    `json:"name"`
    Icon          string    `json:"icon"`
    Color         string    `json:"color"`
    IsHidden      bool      `json:"is_hidden"`
    IsPredefined  bool      `json:"is_predefined"`
    PredefinedKey *string   `json:"predefined_key,omitempty"`
    CreatedAt     time.Time `json:"created_at"`
    UpdatedAt     time.Time `json:"updated_at"`
}
```

**Seed logic:** On user signup, seed the 15 predefined categories into `custom_categories` for that user with `is_predefined = true` and the corresponding `predefined_key`. This ensures users can customize predefined categories (hide, rename, recolor) per-user.

**Delete guard:** If `predefined_key = 'other'`, return 400 — this category cannot be deleted.

### 1.5 Dashboard

**Package:** `internal/dashboard/` (update existing)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/dashboard/monthly?month=3&year=2026` | Monthly overview |

Update queries to use new `expenses` table (not `personal_expenses`), filter `is_deleted = false`.

---

## 🟢 Phase 2: Export APIs (Priority: P1)

### 2.1 Export Expenses

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/export/csv?start_date=...&end_date=...` | Export expenses as CSV |
| GET | `/export/json?start_date=...&end_date=...` | Export expenses as JSON |

**CSV columns:** `id, amount, category, date, time, description, notes, created_at`

**Filters:** `start_date`, `end_date`, `category`

**Response:** Stream the file with `Content-Disposition: attachment` header.

---

## 🟡 Phase 3: Splitwise-like Group Feature (Priority: P2 — Backend Only)

> Client will NOT implement this yet. Build the backend so it's ready when client catches up.

### 3.1 New Tables for Groups

```sql
-- Groups (expense-sharing groups, like Splitwise)
CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

-- Group expenses link to the main expenses table via group_id
-- Splits track how a group expense is divided
CREATE TABLE expense_splits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    expense_id UUID NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount >= 0),
    UNIQUE(expense_id, user_id)
);

CREATE TABLE settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    from_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    to_user UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(12,2) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (from_user != to_user)
);
```

### 3.2 Group APIs

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/groups` | Create group |
| GET | `/groups` | List user's groups (with members + balances inline) |
| GET | `/groups/:id` | Get group details |
| POST | `/groups/:id/members` | Add member by email |
| DELETE | `/groups/:id/members/:user_id` | Remove member |
| GET | `/groups/:id/balances` | Get group balances |
| POST | `/groups/:id/expenses` | Add group expense with splits |
| GET | `/groups/:id/expenses` | List group expenses (paginated) |
| POST | `/settlements` | Record a settlement |
| GET | `/groups/:id/settlements` | List settlements |

### 3.3 Group Expense Flow
1. User creates expense with `group_id` set
2. Expense is inserted into `expenses` table with `group_id` + `group_name`
3. Splits are inserted into `expense_splits` (must sum to total amount)
4. Balances are computed: `paid_by` gets +total, split users get -share
5. Settlements reduce balances

---

## 🔧 Phase 4: Infrastructure & Cleanup (Priority: P1)

### 4.1 Routing Cleanup
- [ ] Remove old `/personal-expenses` routes
- [ ] Remove old group expense routes from `/expenses` (now unified)
- [ ] Update `/dashboard/monthly` queries to use new `expenses` table

### 4.2 API Response Consistency
- [ ] All errors: `{"error": "message"}`
- [ ] All lists: `{"data": [...], "pagination": {"limit": N, "offset": N, "total": N}}`
- [ ] All amounts serialized as strings (keep `helpers.StringDecimal`)
- [ ] All timestamps in ISO 8601 / RFC 3339

### 4.3 Auth
- [ ] Keep existing JWT auth as-is
- [ ] Add `GET /me` endpoint to return current user profile
- [ ] Category seeding on signup (15 predefined categories)

### 4.4 Middleware
- [ ] Keep existing: JWT, CORS, Rate Limiter, Request Logger
- [ ] Add: request ID middleware for tracing

---

## 📋 File Cleanup Checklist

| Action | Old | New |
|--------|-----|-----|
| Replace | `internal/personalexpense/` | `internal/expense/` (unified) |
| Update | `internal/category/category.go` | Match `CustomCategory` model |
| Update | `internal/budget/budget.go` | Match `MonthlyBudget` model |
| Create | — | `internal/recurring/recurring.go` |
| Update | `internal/dashboard/dashboard.go` | Query new `expenses` table |
| Update | `internal/group/group.go` | Cleanup, use shared `expenses` table |
| Update | `cmd/main.go` | New route registration |
| Replace | All migrations | Single fresh `000001_initial.up.sql` |

---

## 📋 Summary — Priority Order

| # | Priority | Task | Package |
|---|----------|------|---------|
| 1 | 🔴 P0 | Fresh DB schema (drop + recreate all tables) | `internal/db/migrations/` |
| 2 | 🔴 P0 | Expenses CRUD (new model with soft delete) | `internal/expense/` |
| 3 | 🔴 P0 | Recurring Expenses CRUD (standalone table) | `internal/recurring/` |
| 4 | 🔴 P0 | Monthly Budgets CRUD (field rename: limit) | `internal/budget/` |
| 5 | 🔴 P0 | Custom Categories CRUD (predefined seeding) | `internal/category/` |
| 6 | 🔴 P0 | Dashboard update (new table, exclude deleted) | `internal/dashboard/` |
| 7 | 🟡 P1 | Export APIs (CSV/JSON) | `internal/export/` |
| 8 | 🟡 P1 | Auth: seed categories on signup + GET /me | `internal/auth/` |
| 9 | 🟡 P1 | Response consistency + routing cleanup | `cmd/main.go` |
| 10 | 🟢 P2 | Splitwise group feature (backend only) | `internal/group/` |
| 11 | 🟢 P2 | Settlements | `internal/settlement/` |
