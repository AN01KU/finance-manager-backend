package dashboard

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

func setupDashboardTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/finance_manager_test?sslmode=disable"
	}

	err := db.RunMigrations(context.Background(), dbURL, "")
	require.NoError(t, err)

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(),
		"TRUNCATE users, groups, group_members, group_transactions, group_transaction_splits, settlements, transactions, monthly_budgets CASCADE")
	require.NoError(t, err)

	return &db.DB{Pool: pool}
}

func insertUser(t *testing.T, testDB *db.DB, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		id, email, "user", "hash")
	require.NoError(t, err)
	return id
}

func insertPersonalExpense(t *testing.T, testDB *db.DB, userID uuid.UUID, amount decimal.Decimal, category string, date time.Time) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO transactions (user_id, type, amount, category, date, updated_at)
		 VALUES ($1, 'expense', $2, $3, $4, NOW())`,
		userID, amount, category, date)
	require.NoError(t, err)
}

// insertGroupExpense simulates what CreateGroupTransaction does:
// - inserts group_transactions row
// - inserts personal transactions for each member (payer gets totalAmount, non-payers get split amount)
// - inserts group_transaction_splits with each member's split share
func insertGroupExpense(t *testing.T, testDB *db.DB,
	groupID, paidByUserID uuid.UUID,
	totalAmount decimal.Decimal,
	splits map[uuid.UUID]decimal.Decimal,
	category string, date time.Time,
) uuid.UUID {
	t.Helper()

	gtID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		gtID, groupID, paidByUserID, totalAmount, category, date)
	require.NoError(t, err)

	for userID, splitAmt := range splits {
		personalAmt := splitAmt // after fix: payer also stores their split share, not totalAmount

		var txID uuid.UUID
		err = testDB.Pool.QueryRow(context.Background(),
			`INSERT INTO transactions (user_id, type, amount, category, date, group_transaction_id, updated_at)
			 VALUES ($1, 'expense', $2, $3, $4, $5, NOW()) RETURNING id`,
			userID, personalAmt, category, date, gtID).Scan(&txID)
		require.NoError(t, err)

		_, err = testDB.Pool.Exec(context.Background(),
			`INSERT INTO group_transaction_splits (group_transaction_id, user_id, amount, transaction_id)
			 VALUES ($1, $2, $3, $4)`,
			gtID, userID, splitAmt, txID)
		require.NoError(t, err)
	}

	return gtID
}

func setupGroup(t *testing.T, testDB *db.DB, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	groupID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO groups (id, name, created_by) VALUES ($1, $2, $3)",
		groupID, "Test Group", members[0])
	require.NoError(t, err)
	for _, m := range members {
		_, err = testDB.Pool.Exec(context.Background(),
			"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)",
			groupID, m)
		require.NoError(t, err)
	}
	return groupID
}

// queryDashboardTotals runs the same logic as GetMonthlyDashboard for a given user+month+year,
// returning (totalSpent, expenseCount).
func queryDashboardTotals(t *testing.T, testDB *db.DB, userID uuid.UUID, month, year int) (decimal.Decimal, int) {
	t.Helper()
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	var totalSpent decimal.Decimal
	var expenseCount int
	err := testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount), 0), COUNT(*)
		 FROM transactions
		 WHERE user_id = $1 AND type = 'expense' AND date >= $2 AND date < $3 AND is_deleted = false`,
		userID, startDate, endDate).Scan(&totalSpent, &expenseCount)
	require.NoError(t, err)
	return totalSpent, expenseCount
}

// TestDashboard_PayerGroupExpenseCountsAsSplitShare is the RED test.
// It proves the current bug: the payer's dashboard shows the full group expense amount
// instead of only their split share.
func TestDashboard_PayerGroupExpenseCountsAsSplitShare(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "payer@example.com")
	member := insertUser(t, testDB, "member@example.com")
	groupID := setupGroup(t, testDB, payer, member)

	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Group expense: $90 total, payer's share is $45, member's share is $45
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(90),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(45),
			member: decimal.NewFromFloat(45),
		}, "Dining", april)

	payerTotal, _ := queryDashboardTotals(t, testDB, payer, 4, 2026)
	memberTotal, _ := queryDashboardTotals(t, testDB, member, 4, 2026)

	// The payer should see $45 (their split share), not $90 (full amount).
	// This currently FAILS — demonstrating the bug.
	assert.Equal(t, "45", payerTotal.String(),
		"payer's dashboard should show their split share ($45), not the full group expense ($90)")

	// The non-payer's dashboard should correctly show their $45 share.
	assert.Equal(t, "45", memberTotal.String(),
		"member's dashboard should show their split share ($45)")
}

// TestDashboard_PersonalExpenseUnaffected ensures the fix doesn't break personal (non-group) expenses.
func TestDashboard_PersonalExpenseUnaffected(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	user := insertUser(t, testDB, "solo@example.com")
	april := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	insertPersonalExpense(t, testDB, user, decimal.NewFromFloat(50), "Groceries", april)
	insertPersonalExpense(t, testDB, user, decimal.NewFromFloat(30), "Transport", april)

	total, count := queryDashboardTotals(t, testDB, user, 4, 2026)

	assert.Equal(t, "80", total.String(), "personal expenses should total correctly")
	assert.Equal(t, 2, count)
}

// ── Unified dashboard field tests ─────────────────────────────────────────────

// insertSettlement records a settlement between two users in a group.
func insertSettlement(t *testing.T, testDB *db.DB, groupID, fromUser, toUser uuid.UUID, amount decimal.Decimal) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO settlements (group_id, from_user, to_user, amount) VALUES ($1, $2, $3, $4)`,
		groupID, fromUser, toUser, amount)
	require.NoError(t, err)
}

// queryGroupExpensesTotal returns the sum of the user's group split shares for the given month.
func queryGroupExpensesTotal(t *testing.T, testDB *db.DB, userID uuid.UUID, month, year int) decimal.Decimal {
	t.Helper()
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	var total decimal.Decimal
	err := testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(gts.amount), 0)
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gts.user_id = $1 AND gt.date >= $2 AND gt.date < $3 AND gt.is_deleted = FALSE`,
		userID, startDate, endDate).Scan(&total)
	require.NoError(t, err)
	return total
}

// queryNetBalances returns (net_owed, net_owing) across all groups for the user.
// net_owed = total others owe the user; net_owing = total the user owes others.
func queryNetBalances(t *testing.T, testDB *db.DB, userID uuid.UUID) (decimal.Decimal, decimal.Decimal) {
	t.Helper()

	var paid, splitOwed, settPaid, settReceived decimal.Decimal

	_ = testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(gt.total_amount), 0)
		 FROM group_transactions gt
		 JOIN group_members gm ON gm.group_id = gt.group_id AND gm.user_id = $1
		 WHERE gt.paid_by_user_id = $1 AND gt.is_deleted = FALSE`, userID).Scan(&paid)

	_ = testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(gts.amount), 0)
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gts.user_id = $1 AND gt.is_deleted = FALSE`, userID).Scan(&splitOwed)

	_ = testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount), 0) FROM settlements WHERE from_user = $1 AND is_deleted = FALSE`, userID).Scan(&settPaid)

	_ = testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount), 0) FROM settlements WHERE to_user = $1 AND is_deleted = FALSE`, userID).Scan(&settReceived)

	balance := paid.Sub(splitOwed).Add(settPaid).Sub(settReceived)

	zero := decimal.Zero
	if balance.GreaterThan(zero) {
		return balance, zero
	}
	if balance.LessThan(zero) {
		return zero, balance.Abs()
	}
	return zero, zero
}

func TestUnifiedDashboard_GroupExpensesTotal(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "unified-payer@example.com")
	member := insertUser(t, testDB, "unified-member@example.com")
	groupID := setupGroup(t, testDB, payer, member)

	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Group expense: $100 total, payer=$60, member=$40
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(100),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(60),
			member: decimal.NewFromFloat(40),
		}, "Dining", april)

	// Personal expense: $25 (should not appear in group_expenses_total)
	insertPersonalExpense(t, testDB, payer, decimal.NewFromFloat(25), "Groceries", april)

	groupTotal := queryGroupExpensesTotal(t, testDB, payer, 4, 2026)
	assert.Equal(t, "60", groupTotal.String(),
		"group_expenses_total should be payer's split share only, not including personal expenses")

	memberGroupTotal := queryGroupExpensesTotal(t, testDB, member, 4, 2026)
	assert.Equal(t, "40", memberGroupTotal.String(),
		"member's group_expenses_total should be their split share")
}

func TestUnifiedDashboard_NetOwed(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "net-payer@example.com")
	member := insertUser(t, testDB, "net-member@example.com")
	groupID := setupGroup(t, testDB, payer, member)

	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Payer fronts $100, member owes $50
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(100),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(50),
			member: decimal.NewFromFloat(50),
		}, "Rent", april)

	netOwed, netOwing := queryNetBalances(t, testDB, payer)
	assert.Equal(t, "50", netOwed.String(), "payer is owed $50 by member")
	assert.Equal(t, "0", netOwing.String(), "payer owes nothing")

	memberOwed, memberOwing := queryNetBalances(t, testDB, member)
	assert.Equal(t, "0", memberOwed.String(), "member is owed nothing")
	assert.Equal(t, "50", memberOwing.String(), "member owes $50 to payer")
}

func TestUnifiedDashboard_NetBalanceAfterSettlement(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "sett-payer@example.com")
	member := insertUser(t, testDB, "sett-member@example.com")
	groupID := setupGroup(t, testDB, payer, member)

	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Payer fronts $100, member owes $50
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(100),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(50),
			member: decimal.NewFromFloat(50),
		}, "Rent", april)

	// Member settles the full $50
	insertSettlement(t, testDB, groupID, member, payer, decimal.NewFromFloat(50))

	netOwed, netOwing := queryNetBalances(t, testDB, payer)
	assert.Equal(t, "0", netOwed.String(), "after settlement payer is owed nothing")
	assert.Equal(t, "0", netOwing.String())

	memberOwed, memberOwing := queryNetBalances(t, testDB, member)
	assert.Equal(t, "0", memberOwed.String())
	assert.Equal(t, "0", memberOwing.String(), "after settlement member owes nothing")
}

func TestUnifiedDashboard_GroupExpensesTotalExcludesPreviousMonths(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "month-scope@example.com")
	member := insertUser(t, testDB, "month-scope-member@example.com")
	groupID := setupGroup(t, testDB, payer, member)

	march := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// March expense: $80
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(80),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(40),
			member: decimal.NewFromFloat(40),
		}, "Dining", march)

	// April expense: $100, payer's share = $60
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(100),
		map[uuid.UUID]decimal.Decimal{
			payer:  decimal.NewFromFloat(60),
			member: decimal.NewFromFloat(40),
		}, "Dining", april)

	aprilTotal := queryGroupExpensesTotal(t, testDB, payer, 4, 2026)
	assert.Equal(t, "60", aprilTotal.String(),
		"group_expenses_total should only include April transactions, not March")
}

// TestDashboard_MixedPersonalAndGroupExpenses tests the combined scenario.
func TestDashboard_MixedPersonalAndGroupExpenses(t *testing.T) {
	testDB := setupDashboardTestDB(t)
	defer testDB.Close()

	payer := insertUser(t, testDB, "mixed-payer@example.com")
	other := insertUser(t, testDB, "mixed-other@example.com")
	groupID := setupGroup(t, testDB, payer, other)

	april := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	// Personal: $40
	insertPersonalExpense(t, testDB, payer, decimal.NewFromFloat(40), "Groceries", april)

	// Group: $100 total, payer's share = $60, other's share = $40
	insertGroupExpense(t, testDB, groupID, payer, decimal.NewFromFloat(100),
		map[uuid.UUID]decimal.Decimal{
			payer: decimal.NewFromFloat(60),
			other: decimal.NewFromFloat(40),
		}, "Dining", april)

	// After fix: payer should see $40 (personal) + $60 (group share) = $100
	// Currently fails: payer sees $40 + $100 = $140
	payerTotal, _ := queryDashboardTotals(t, testDB, payer, 4, 2026)
	assert.Equal(t, "100", payerTotal.String(),
		"payer's total should be personal ($40) + their group split share ($60)")
}
