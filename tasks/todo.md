# Category Key Migration — Task List

## PHASE 1 — Schema

- [ ] **T1** `000005_category_keys.up.sql` + `.down.sql`
  - Add `key VARCHAR(120)` to `custom_categories`
  - Backfill existing rows (non-predefined: `username-cc-id`, override: `override-predefinedKey-userID`)
  - Set NOT NULL, add UNIQUE INDEX `(user_id, key)`
  - ✅ Verify: migration runs clean on fresh DB; `SELECT COUNT(*) FROM custom_categories WHERE key IS NULL` = 0

---

## CHECKPOINT A — fresh DB + seed runs without errors

---

## PHASE 2 — Core helper

- [ ] **T2** `internal/helpers/category.go` (new file)
  - `ResolveCategoryKey(ctx, pool, userID, key) (string, error)`
  - UNION query: predefined visible OR user's own custom visible
  - Returns `"other"` if no match
  - ✅ Verify: `go build ./...` passes; unit tests for valid predefined, hidden predefined, valid custom, other user's custom, unknown key

---

## PHASE 3 — Category package

- [ ] **T3** `internal/category/category.go`
  - Add `Key string \`json:"key"\`` to `Category` struct
  - `CreateCategory`: generate key, store + return it
  - `ListCategories`: scan key for custom rows; set key from predefined_key for overrides and virtuals
  - `UpdateCategory`: RETURNING scans include key; key never updated
  - `DeleteCategory`: wrap in tx; cascade UPDATE all 3 txn tables to `'other'` before DELETE
  - ✅ Verify: `POST /categories` response has `key`; `GET /categories` all items have `key`; delete cascades in DB

- [ ] **T4** `internal/category/admin.go`
  - Hard-delete: add 3 UPDATE statements inside the existing tx before deleting the predefined row
  - ✅ Verify: transactions referencing the deleted predefined key show `category='other'` after hard delete

---

## CHECKPOINT B — category API fully operational with keys

---

## PHASE 4 — Write handlers

- [ ] **T5** `internal/transaction/transaction.go`
  - `CreateTransaction`: resolve category key before INSERT
  - `UpdateTransaction`: resolve when `req.Category != nil`
  - ✅ Verify: POST with invalid key stores `'other'`; valid key stores unchanged

- [ ] **T6** `internal/group/group_transactions.go`
  - `CreateGroupTransaction`: resolve before DB tx begins
  - `UpdateGroupTransaction`: resolve when `req.Category != nil`
  - ✅ Verify: same as T5

- [ ] **T7** `internal/recurring/recurring.go`
  - `CreateRecurringTransaction`: resolve after validation, before INSERT
  - `UpdateRecurringTransaction`: resolve when `req.Category != nil`
  - ✅ Verify: same as T5

- [ ] **T8** `internal/settlement/settlement.go`
  - Replace all 4 `'Debt & Payments'` → `'other'`
  - ✅ Verify: create settlement with excess; linked transactions have `category='other'`

---

## CHECKPOINT C — all write endpoints normalise category to a key; read endpoints return keys

---

## PHASE 5 — Seed + Portal

- [ ] **T9** `internal/seed/seed.go`
  - Replace all display name strings with predefined keys (see mapping in plan.md)
  - ✅ Verify: seed runs; `SELECT DISTINCT category FROM transactions` returns only valid predefined keys

- [ ] **T10** `internal/portal/portal.go`
  - Add `buildCategoryNameMap(ctx, userID) map[string]string`
  - `dashboardPage`: translate category breakdown keys → names
  - `transactionsPage`: translate tx category keys → names; filter dropdown uses `{Key, Name}`; translate `filterCategory` key → name for display
  - `transactionsExport`: translate keys → names in CSV
  - `categoriesPage`: fix JOIN to use `cc.key` / `cc.predefined_key` instead of `cc.name`
  - `recurringPage`: translate keys → names
  - `profilePage`: translate top categories keys → names
  - `groupDetailPage`: translate keys → names
  - ✅ Verify: portal pages show display names not raw keys; category filter works; CSV has display names

---

## CHECKPOINT D — full end-to-end pass; go build + go vet + unit tests all pass
