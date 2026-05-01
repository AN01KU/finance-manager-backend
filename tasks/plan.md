# Category Key Migration — Implementation Plan

## Goal
Switch all three transaction tables from storing category display names to storing stable category keys.
- Predefined keys: `food-dining`, `other`, etc. (already in `predefined_categories.key`)
- Custom keys: `{username}-cc-{uuid}` — new `key` column in `custom_categories`
- Unknown/orphaned keys → resolved to `other` at write time

---

## Dependency Order

```
000005 migration SQL
    ↓
internal/helpers/category.go  (new — ResolveCategoryKey)
    ↓
internal/category/category.go  (Key field + create/list/delete cascade)
internal/category/admin.go     (hard-delete cascade)
    ↓
internal/transaction/transaction.go     ─┐
internal/group/group_transactions.go    ─┤ (parallel)
internal/recurring/recurring.go         ─┘
internal/settlement/settlement.go
    ↓
internal/seed/seed.go
    ↓
internal/portal/portal.go
```

---

## Phase 1 — Schema (migration 000005)

**Files:** `000005_category_keys.up.sql`, `000005_category_keys.down.sql`

Changes:
- Add `key VARCHAR(120)` to `custom_categories` (nullable initially)
- Backfill `is_predefined=FALSE` rows: `key = username || '-cc-' || id::text`
- Backfill `is_predefined=TRUE` rows: `key = 'override-' || predefined_key || '-' || user_id::text`
- Set NOT NULL
- Add `UNIQUE INDEX (user_id, key)`

No changes to `transactions`, `group_transactions`, or `recurring_transactions` column types — semantics change only.

---

## Phase 2 — `internal/helpers/category.go` (new file)

```go
func ResolveCategoryKey(ctx, pool, userID, key) (string, error)
```

Resolution logic (single UNION query):
1. `predefined_categories WHERE key=$1 AND is_hidden=FALSE`
2. OR `custom_categories WHERE key=$1 AND user_id=$2 AND is_predefined=FALSE AND is_hidden=FALSE`
3. If no row found → return `"other"`

Used by: transaction, group txn, recurring create/update handlers.

---

## Phase 3 — `internal/category/category.go`

- Add `Key string \`json:"key"\`` to `Category` struct
- `CreateCategory`: generate `key = username + "-cc-" + uuid.New().String()`, store in INSERT, scan in RETURNING
- `ListCategories`:
  - Custom rows: scan `key` from DB
  - Predefined override rows: `Key = *cat.PredefinedKey`
  - Virtual predefined entries: `Key = p.Key`
- `UpdateCategory`: PATCH never modifies key; override INSERT uses `"override-" + matchedKey + "-" + userID.String()`; RETURNING scans include `key`
- `DeleteCategory`: wrap in tx; before DELETE run:
  ```sql
  UPDATE transactions SET category='other' WHERE category=$key AND user_id=$userID
  UPDATE group_transactions SET category='other' WHERE category=$key AND paid_by_user_id=$userID
  UPDATE recurring_transactions SET category='other' WHERE category=$key AND user_id=$userID
  ```

---

## Phase 4 — `internal/category/admin.go`

In `AdminDeletePredefined` hard-delete transaction, add before deleting the predefined row:
```sql
UPDATE transactions SET category='other' WHERE category=$key
UPDATE group_transactions SET category='other' WHERE category=$key
UPDATE recurring_transactions SET category='other' WHERE category=$key
```
No user scope — predefined keys are global.

---

## Phase 5 — Transaction handlers

### `transaction.go`
- `CreateTransaction`: after binding req, call `ResolveCategoryKey(userID, req.Category)`, use result in INSERT
- `UpdateTransaction`: when `req.Category != nil`, resolve before adding to UPDATE args

### `group_transactions.go`
- `CreateGroupTransaction`: resolve category before the DB transaction begins
- `UpdateGroupTransaction`: same for `req.Category != nil`

### `recurring/recurring.go`
- `CreateRecurringTransaction`: resolve after validation, before INSERT
- `UpdateRecurringTransaction`: same for `req.Category != nil`

### `settlement/settlement.go`
- Change all 4 occurrences of `'Debt & Payments'` → `'other'`

---

## Phase 6 — Seed data

Replace all display name strings in `seed.go` with predefined keys:

| Display Name | Key |
|---|---|
| `"Food & Dining"` | `"food-dining"` |
| `"Transport"` | `"transport"` |
| `"Housing & Rent"` | `"housing-rent"` |
| `"Gym & Fitness"` | `"gym-fitness"` |
| `"Salary & Income"` | `"salary-income"` |
| `"Dining Out"` | `"dining-out"` |
| `"Clothing"` | `"clothing"` |
| `"Pharmacy"` | `"pharmacy"` |
| `"Coffee & Cafe"` | `"coffee-cafe"` |
| `"Entertainment"` | `"entertainment"` |
| `"Streaming"` | `"streaming"` |
| `"Public Transit"` | `"public-transit"` |
| `"Groceries"` | `"groceries"` |
| `"Online Courses"` | `"online-courses"` |
| `"Shopping"` | `"shopping"` |
| `"Electricity & Gas"` | `"electricity-gas"` |
| `"Freelance"` | `"freelance"` |
| `"Health & Medical"` | `"health-medical"` |
| `"Books & Reading"` | `"books-reading"` |
| `"Other"` | `"other"` |
| `"Travel"` | `"travel"` |
| `"Phone & Internet"` | `"phone-internet"` |
| `"Debt & Payments"` | `"other"` |

---

## Phase 7 — `internal/portal/portal.go`

Add a private helper:
```go
func (p *Portal) buildCategoryNameMap(ctx, userID) map[string]string
```
Loads: predefined names → overridden by user override rows → plus custom category names.

Apply at:
- `dashboardPage`: translate `r.Category` key → name in category breakdown
- `transactionsPage`:
  - Translate each transaction's `tx.Category` key → name for display
  - Filter dropdown: populate with `{Key, Name}` structs (key for form value, name for label)
  - `filterCategory` query param is now a key; used as-is in SQL (correct), shown as display name in UI
- `transactionsExport`: translate key → name for human-readable CSV
- `categoriesPage`: fix JOIN — `t.category = CASE WHEN cc.is_predefined THEN cc.predefined_key ELSE cc.key END`
- `recurringPage`: translate `r.Category` key → name
- `profilePage`: translate top categories keys → names
- `groupDetailPage`: translate `gt.category` key → name

---

## Edge Cases

1. **Override rows** must have a non-null `key` (use synthetic value) but are never addressed by that key in resolution
2. **Group transactions** with another user's custom key → `ResolveCategoryKey` for the viewing user won't find it → falls back to `other` — correct
3. **Portal filter dropdown** value attribute must be the key; display text is the name
4. **CSV export** should show display names not keys (human-readable)
5. **`"other"` is always valid** — `ResolveCategoryKey` must special-case it or rely on it always being present in `predefined_categories`
