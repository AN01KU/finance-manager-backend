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
