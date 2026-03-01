Backend Fixes Needed

🔴 1. decimal.Decimal serializes as JSON number — iOS expects JSON string

This is the biggest cross-cutting issue. shopspring/decimal marshals as 1234.56 (number), but every iOS model declares amounts as String and expects "1234.56" (quoted string).

Affected files/structs:

File
Fields
expense/expense.go
Expense.TotalAmount, ExpenseSplit.Amount
group/group.go
Balance.Amount
settlement/settlement.go
Settlement.Amount
budget/budget.go
MonthlyBudget.Amount
personalexpense/expense.go
PersonalExpense.Amount
dashboard/dashboard.go
MonthlyDashboard.TotalSpent, .Budget, .RemainingBudget, .DailyAverageSpent, .ProjectedSpending, CategorySpending.TotalAmount

Fix: Create a StringDecimal wrapper type that marshals/unmarshals as a JSON string:

// internal/helpers/decimal.go
type StringDecimal struct {
    decimal.Decimal
}

func (d StringDecimal) MarshalJSON() ([]byte, error) {
    return []byte(`"` + d.String() + `"`), nil
}

func (d *StringDecimal) UnmarshalJSON(data []byte) error {
    s := strings.Trim(string(data), `"`)
    dec, err := decimal.NewFromString(s)
    if err != nil {
        return err
    }
    d.Decimal = dec
    return nil
}

Then replace all decimal.Decimal JSON fields with StringDecimal.

---

🔴 2. Shared expenses table missing category column

iOS sends category in CreateSharedExpenseRequest and expects it back in SharedExpense. The expenses DB table and Go structs have no category field.

Files: expense/expense.go, needs new migration

Fix:

New migration: ALTER TABLE expenses ADD COLUMN category VARCHAR(50);
Add Category string json:"category" to Expense struct
Add Category string json:"category" to CreateExpenseRequest struct
Update INSERT/SELECT queries to include category

---

🔴 3. personalexpense/expense.go ListExpenses — SQL references dropped category_id column

Line 113:

SELECT id, user_id, category_id, amount, ...

Migration 000003 dropped category_id and added category VARCHAR(50).

Fix: Change category_id → category in the SELECT.

---

🔴 4. dashboard/dashboard.go — SQL references dropped category_id and invalid JOIN

Lines 90-95:

SELECT pe.category_id, ec.name, ...
FROM personal_expenses pe
LEFT JOIN expense_categories ec ON pe.category_id = ec.id

category_id no longer exists. The category column is now a plain VARCHAR, not a FK.

Fix: Change to:

SELECT pe.category, pe.category, COALESCE(SUM(pe.amount), 0), COUNT(*)
FROM personal_expenses pe
WHERE pe.user_id = $1 AND pe.expense_date >= $2 AND pe.expense_date < $3
GROUP BY pe.category
ORDER BY SUM(pe.amount) DESC

And map category to both CategoryID (set to nil) and CategoryName.

Actually, iOS expects:

struct CategoryBreakdown: Decodable, Sendable {
    let categoryId: UUID?
    let categoryName: String?
    let totalAmount: String
    let expenseCount: Int
}

So category_id can be null and category_name should be the string category name.

---

🔴 5. Settlement balance logic is reversed in group/group.go GetBalances

Lines 254-259:

members[from] = bal.Sub(amt)  // Wrong
members[to] = bal.Add(amt)    // Wrong

When from_user pays to_user, from's debt decreases (balance should go up), to's credit decreases (balance should go down).

Fix:

members[from] = bal.Add(amt)
members[to] = bal.Sub(amt)

---

🔴 6. Category color/icon use *string + omitempty — iOS expects non-optional String

File: category/category.go

Go's ExpenseCategory:

Color *string `json:"color,omitempty"`
Icon  *string `json:"icon,omitempty"`

iOS's CategoryResponse:

let color: String  // non-optional
let icon: String   // non-optional

If color or icon is null in DB, Go omits the field, iOS decode fails.

Fix: Change to non-pointer with no omitempty:

Color string `json:"color"`
Icon  string `json:"icon"`

And ensure DB defaults (e.g., COALESCE(color, '') in queries or DEFAULT '' in schema).

---

🔴 7. Dashboard projected_spending can be null — iOS expects non-optional String

File: dashboard/dashboard.go

Go returns ProjectedSpending *decimal.Decimal which can be nil. iOS has:

let projectedSpending: String  // non-optional!

Fix: Always return a value — default to "0" when no budget or no days elapsed:

if projectedSpending == nil {
    zero := StringDecimal{decimal.Zero}
    projectedSpending = &zero
}

---

🔴 6. Category color/icon use *string + omitempty — iOS expects non-optional String

File: category/category.go

Go's ExpenseCategory:

Color *string `json:"color,omitempty"`
Icon  *string `json:"icon,omitempty"`

iOS's CategoryResponse:

let color: String  // non-optional
let icon: String   // non-optional

If color or icon is null in DB, Go omits the field, iOS decode fails.

Fix: Change to non-pointer with no omitempty:

Color string `json:"color"`
Icon  string `json:"icon"`

And ensure DB defaults (e.g., COALESCE(color, '') in queries or DEFAULT '' in schema).

---

🔴 7. Dashboard projected_spending can be null — iOS expects non-optional String

File: dashboard/dashboard.go

Go returns ProjectedSpending *decimal.Decimal which can be nil. iOS has:

let projectedSpending: String  // non-optional!

Fix: Always return a value — default to "0" when no budget or no days elapsed:

if projectedSpending == nil {
    zero := StringDecimal{decimal.Zero}
    projectedSpending = &zero
}

---

```
Summary Table

#
Severity
File(s)
Issue
1
🔴 Critical
All handlers
decimal.Decimal → JSON number, iOS expects string
2
🔴 Critical
expense/expense.go + migration
Shared expenses missing category
3
🔴 Critical
personalexpense/expense.go:113
SQL uses dropped category_id column
4
🔴 Critical
dashboard/dashboard.go:90
SQL uses dropped category_id + invalid JOIN
5
🔴 Critical
group/group.go:254-259
Settlement balance Add/Sub reversed
6
🔴 Breaking
category/category.go
color/icon omitted when null → iOS decode fails
7
🔴 Breaking
dashboard/dashboard.go
projected_spending null → iOS decode fails
8
🟡 Medium
category/category.go
Request field types: pointer vs value
9
🟡 Medium
settlement/settlement.go
Amount field type + broken validator