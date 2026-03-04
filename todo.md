# Backend TODO — API Restructure + Fixes

---

## 🔵 API Restructure: Rich Responses (Eliminate N+1 Calls)

### 1. `GET /groups` — Return full metadata with members & balances

**Current:** Returns `[{id, name, created_by, created_at}]` — iOS makes 3 extra calls per group.

**Target response:**
```json
[
  {
    "id": "uuid",
    "name": "Trip Fund",
    "created_by": "uuid",
    "created_at": "2025-01-01T00:00:00Z",
    "members": [
      { "id": "uuid", "email": "a@b.com", "username": "alice", "created_at": "..." }
    ],
    "balances": [
      { "user_id": "uuid", "amount": "150.00" }
    ]
  }
]
```

**Files:** `internal/group/group.go` — Update `GetUserGroups()`:
- [x] Add `Members []GroupMember` and `Balances []Balance` to a new `GroupWithDetails` response struct
- [x] After fetching groups, batch-fetch all members for those group IDs in one query
- [x] Batch-fetch all balances (or compute inline)
- [x] Return the enriched response

**Note:** Expenses are intentionally NOT embedded — they stay at `GET /groups/:id/expenses` since they're paginated and loaded on-demand.

---

### 2. `GET /groups/:id/members` — Response wrapper mismatch

**Current:** Returns `{"members": [...]}` but iOS was trying to decode as `[APIUser]`.

**Fix (iOS side done):** iOS now decodes `GroupMembersResponse` wrapper. Backend is fine as-is.

- [x] iOS updated to decode `{"members": [...]}` wrapper

---

## 🔄 Recurring Expenses Enhancements

### 3. Add `days_of_week` field for weekly recurring expenses

**Current:** Only `day_of_month` exists. Weekly recurring has no way to specify which days.

**Target:** Add `days_of_week INTEGER[]` column (array of ints: 0=Sun, 1=Mon, ..., 6=Sat).

**Files:**
- [x] New migration: `ALTER TABLE personal_expenses ADD COLUMN days_of_week INTEGER[];`
- [x] `internal/personalexpense/expense.go`:
  - Add `DaysOfWeek []int` to `PersonalExpense` struct
  - Add `DaysOfWeek []int` to `CreateExpenseRequest` and `UpdateExpenseRequest`
  - Update all INSERT/SELECT/UPDATE queries to include `days_of_week`
  - Validate: if `frequency == "weekly"`, `days_of_week` must be non-empty with values 0-6

---

### 4. Add `?recurring=true/false` filter to `GET /personal-expenses`

**Current:** No way to filter by recurring status.

**Files:** `internal/personalexpense/expense.go` — Update `ListExpenses()`:
- [x] Parse `recurring` query param
- [x] Add `AND is_recurring = $N` to query and count query when present

---

## 🔴 Critical Existing Bugs (from previous audit)

### 5. `decimal.Decimal` serializes as JSON number — iOS expects string

**Status:** Already fixed with `helpers.StringDecimal` wrapper — verify all fields use it.

- [x] Expense.TotalAmount, ExpenseSplit.Amount
- [x] Balance.Amount
- [x] Settlement.Amount
- [x] MonthlyBudget.Amount
- [x] PersonalExpense.Amount
- [x] Dashboard fields

---

### 6. Settlement balance logic in `GetBalances`

Lines 254-259 in `group.go`:
```go
members[from] = bal.Add(amt)  // from_user paid, their balance goes up
members[to] = bal.Sub(amt)    // to_user received, their balance goes down
```

- [x] Already fixed — verify with test

---

### 7. Category `color`/`icon` — `*string` + `omitempty` causes iOS decode failure

**File:** `internal/category/category.go`

- [x] Change `Color *string` → `Color string` (no pointer, no omitempty)
- [x] Change `Icon *string` → `Icon string`
- [x] Use `COALESCE(color, '') AS color` in SELECT queries
- [x] Or add `DEFAULT ''` to DB column

---

### 8. Dashboard `projected_spending` null handling

**File:** `internal/dashboard/dashboard.go`

- [x] Already has nil → zero fallback at line 154-157

---

## 🟡 API Design Improvements

### 9. `GET /groups/:id` — already returns full detail, but verify consistency

**Current:** Returns `{group: {id, name, members, expenses}, is_member: true}`.

- [x] Ensure `GroupDetails` struct includes `Balances` too (currently missing)
- [x] Add `Balances []Balance` to `GroupDetails` and compute in `GetGroup()`

---

### 10. Pagination consistency

- [ ] `GET /personal-expenses` ✅ has pagination
- [ ] `GET /groups/:id/expenses` ✅ has pagination
- [ ] `GET /budgets` ❌ no pagination (low priority — users won't have hundreds)
- [ ] `GET /categories` ❌ no pagination (low priority — typically < 20)

---

### 11. Error response consistency

All errors should follow `{"error": "message"}` format consistently.
- [ ] Audit all handlers return consistent error shape

---

## 📋 Summary — Priority Order

| # | Priority | Task | Files |
|---|----------|------|-------|
| 1 | 🔴 P0 | Rich `GET /groups` (members + balances inline) | `group/group.go` |
| 3 | 🔴 P0 | Add `days_of_week` for weekly recurring | migration + `personalexpense/expense.go` |
| 4 | 🟡 P1 | Add `?recurring` filter to personal expenses | `personalexpense/expense.go` |
| 7 | 🟡 P1 | Fix category color/icon null handling | `category/category.go` |
| 9 | 🟡 P1 | Add balances to `GET /groups/:id` response | `group/group.go` |
| 10 | 🟢 P2 | Pagination for budgets/categories | `budget/budget.go`, `category/category.go` |
| 11 | 🟢 P2 | Error response consistency audit | All handlers |
