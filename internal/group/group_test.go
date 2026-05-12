package group

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

func setupBalanceTestDB(t *testing.T) *db.DB {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/finance_manager_test?sslmode=disable"
	}

	err := db.RunMigrations(context.Background(), dbURL, "")
	require.NoError(t, err)

	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)

	// Clean up tables
	_, err = pool.Exec(context.Background(), "TRUNCATE users, groups, group_members, group_transactions, group_transaction_splits, settlements CASCADE")
	require.NoError(t, err)

	return &db.DB{Pool: pool}
}

func createBalanceTestUser(t *testing.T, testDB *db.DB, email string) uuid.UUID {
	userID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO users (id, email, username, password_hash) VALUES ($1, $2, $3, $4)",
		userID, email, "testuser", "hashedpassword")
	require.NoError(t, err)
	return userID
}

func createBalanceTestGroup(t *testing.T, testDB *db.DB, creatorID uuid.UUID) uuid.UUID {
	groupID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO groups (id, name, created_by) VALUES ($1, $2, $3)",
		groupID, "Test Group", creatorID)
	require.NoError(t, err)

	// Add creator as member
	_, err = testDB.Pool.Exec(context.Background(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)",
		groupID, creatorID)
	require.NoError(t, err)

	return groupID
}

func addGroupMember(t *testing.T, testDB *db.DB, groupID, userID uuid.UUID) {
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)",
		groupID, userID)
	require.NoError(t, err)
}

func createGroupTx(t *testing.T, testDB *db.DB, groupID, paidBy uuid.UUID, total decimal.Decimal, splits map[uuid.UUID]decimal.Decimal) uuid.UUID {
	gtID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, description) VALUES ($1, $2, $3, $4, 'Other', NOW(), 'Test Expense')",
		gtID, groupID, paidBy, total)
	require.NoError(t, err)

	for userID, amount := range splits {
		_, err := testDB.Pool.Exec(context.Background(),
			"INSERT INTO group_transaction_splits (group_transaction_id, user_id, amount) VALUES ($1, $2, $3)",
			gtID, userID, amount)
		require.NoError(t, err)
	}

	return gtID
}

func createSettlement(t *testing.T, testDB *db.DB, groupID, fromUser, toUser uuid.UUID, amount decimal.Decimal) uuid.UUID {
	settlementID := uuid.New()
	_, err := testDB.Pool.Exec(context.Background(),
		"INSERT INTO settlements (id, group_id, from_user, to_user, amount) VALUES ($1, $2, $3, $4, $5)",
		settlementID, groupID, fromUser, toUser, amount)
	require.NoError(t, err)
	return settlementID
}

func TestBalanceCalculation(t *testing.T) {
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	userA := createBalanceTestUser(t, testDB, "a@example.com")
	userB := createBalanceTestUser(t, testDB, "b@example.com")
	userC := createBalanceTestUser(t, testDB, "c@example.com")

	groupID := createBalanceTestGroup(t, testDB, userA)
	addGroupMember(t, testDB, groupID, userB)
	addGroupMember(t, testDB, groupID, userC)

	tests := []struct {
		name     string
		setup    func()
		expected map[uuid.UUID]decimal.Decimal
	}{
		{
			name: "simple expense - A pays 100, split equally",
			setup: func() {
				createGroupTx(t, testDB, groupID, userA, decimal.NewFromFloat(100),
					map[uuid.UUID]decimal.Decimal{
						userA: decimal.NewFromFloat(33.33),
						userB: decimal.NewFromFloat(33.33),
						userC: decimal.NewFromFloat(33.34),
					})
			},
			expected: map[uuid.UUID]decimal.Decimal{
				userA: decimal.NewFromFloat(100).Sub(decimal.NewFromFloat(33.33)), // ~66.67
				userB: decimal.NewFromFloat(0).Sub(decimal.NewFromFloat(33.33)),   // ~-33.33
				userC: decimal.NewFromFloat(0).Sub(decimal.NewFromFloat(33.34)),   // ~-33.34
			},
		},
		{
			name: "settlement reduces balance",
			setup: func() {
				createGroupTx(t, testDB, groupID, userA, decimal.NewFromFloat(60),
					map[uuid.UUID]decimal.Decimal{
						userA: decimal.NewFromFloat(20),
						userB: decimal.NewFromFloat(20),
						userC: decimal.NewFromFloat(20),
					})
				createSettlement(t, testDB, groupID, userB, userA, decimal.NewFromFloat(20))
			},
			expected: map[uuid.UUID]decimal.Decimal{
				userA: decimal.NewFromFloat(60).Sub(decimal.NewFromFloat(20)).Sub(decimal.NewFromFloat(20)), // 60 - 20 (split) - 20 (received settlement) = 20
				userB: decimal.NewFromFloat(0).Sub(decimal.NewFromFloat(20)).Add(decimal.NewFromFloat(20)),  // 0 - 20 (split) + 20 (paid settlement) = 0
				userC: decimal.NewFromFloat(0).Sub(decimal.NewFromFloat(20)),                                // -20
			},
		},
		{
			name: "multiple expenses and settlements",
			setup: func() {
				// Expense 1: A pays 90, split equally
				createGroupTx(t, testDB, groupID, userA, decimal.NewFromFloat(90),
					map[uuid.UUID]decimal.Decimal{
						userA: decimal.NewFromFloat(30),
						userB: decimal.NewFromFloat(30),
						userC: decimal.NewFromFloat(30),
					})
				// Expense 2: B pays 60, split equally
				createGroupTx(t, testDB, groupID, userB, decimal.NewFromFloat(60),
					map[uuid.UUID]decimal.Decimal{
						userA: decimal.NewFromFloat(20),
						userB: decimal.NewFromFloat(20),
						userC: decimal.NewFromFloat(20),
					})
				// Settlement: C pays A 10
				createSettlement(t, testDB, groupID, userC, userA, decimal.NewFromFloat(10))
			},
			expected: map[uuid.UUID]decimal.Decimal{
				userA: decimal.NewFromFloat(90).Sub(decimal.NewFromFloat(30)).Sub(decimal.NewFromFloat(20)).Sub(decimal.NewFromFloat(10)), // 90 - 30 (split) - 20 (split) - 10 (received settlement) = 30
				userB: decimal.NewFromFloat(60).Sub(decimal.NewFromFloat(20)).Sub(decimal.NewFromFloat(30)),                               // 60 - 20 (split) - 30 (split) = 10
				userC: decimal.NewFromFloat(0).Sub(decimal.NewFromFloat(30)).Sub(decimal.NewFromFloat(20)).Add(decimal.NewFromFloat(10)),  // -30 - 20 + 10 (paid settlement) = -40
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up for each test
			_, err := testDB.Pool.Exec(context.Background(), "TRUNCATE group_transactions, group_transaction_splits, settlements CASCADE")
			require.NoError(t, err)

			tt.setup()

			// Calculate balances by simulating the logic
			balances := make(map[uuid.UUID]decimal.Decimal)
			balances[userA] = decimal.Zero
			balances[userB] = decimal.Zero
			balances[userC] = decimal.Zero

			// Add from group_transactions
			expRows, err := testDB.Pool.Query(context.Background(),
				"SELECT paid_by_user_id, total_amount FROM group_transactions WHERE group_id = $1 AND is_deleted = FALSE", groupID)
			require.NoError(t, err)
			defer expRows.Close()

			for expRows.Next() {
				var paidBy uuid.UUID
				var total decimal.Decimal
				err := expRows.Scan(&paidBy, &total)
				require.NoError(t, err)
				balances[paidBy] = balances[paidBy].Add(total)
			}

			// Subtract splits
			splitRows, err := testDB.Pool.Query(context.Background(),
				`SELECT gts.user_id, gts.amount FROM group_transaction_splits gts
				 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
				 WHERE gt.group_id = $1 AND gt.is_deleted = FALSE`, groupID)
			require.NoError(t, err)
			defer splitRows.Close()

			for splitRows.Next() {
				var uid uuid.UUID
				var amt decimal.Decimal
				err := splitRows.Scan(&uid, &amt)
				require.NoError(t, err)
				balances[uid] = balances[uid].Sub(amt)
			}

			// Adjust settlements: from_user paid off debt (+), to_user received payment (-)
			settRows, err := testDB.Pool.Query(context.Background(),
				"SELECT from_user, to_user, amount FROM settlements WHERE group_id = $1", groupID)
			require.NoError(t, err)
			defer settRows.Close()

			for settRows.Next() {
				var from, to uuid.UUID
				var amt decimal.Decimal
				err := settRows.Scan(&from, &to, &amt)
				require.NoError(t, err)
				balances[from] = balances[from].Add(amt)
				balances[to] = balances[to].Sub(amt)
			}

			// Check expectations (allowing small decimal differences)
			for userID, expectedBalance := range tt.expected {
				actual := balances[userID]
				diff := expectedBalance.Sub(actual).Abs()
				assert.True(t, diff.LessThan(decimal.NewFromFloat(0.01)),
					"Balance for user %s: expected %s, got %s", userID, expectedBalance, actual)
			}
		})
	}
}

// TestAddMember_AntiEnumerationErrorShape verifies that the two failure
// modes — email is not a registered user, vs email belongs to a user who
// is already a member — return the same generic 400 error code so a
// curious member cannot probe whether arbitrary email addresses have an
// account on this server.
func TestAddMember_AntiEnumerationErrorShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	creator := createBalanceTestUser(t, testDB, "creator-anti@example.com")
	already := createBalanceTestUser(t, testDB, "already-anti@example.com")
	groupID := createBalanceTestGroup(t, testDB, creator)
	addGroupMember(t, testDB, groupID, already)

	call := func(email string) (int, map[string]interface{}) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", creator)
		c.Params = gin.Params{{Key: "id", Value: groupID.String()}}
		body, _ := json.Marshal(AddMemberRequest{Email: email})
		c.Request = httptest.NewRequest("POST",
			"/groups/"+groupID.String()+"/members", bytes.NewBuffer(body))
		c.Request.Header.Set("Content-Type", "application/json")
		AddMember(c, testDB)
		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}

	statusUnknown, bodyUnknown := call("noone-anti@example.com")
	statusAlready, bodyAlready := call("already-anti@example.com")

	assert.Equal(t, statusUnknown, statusAlready,
		"unknown-email and already-member must return the same HTTP status")
	assert.Equal(t, 400, statusUnknown, "expected anti-enumeration 400")
	assert.Equal(t, bodyUnknown["code"], bodyAlready["code"],
		"unknown-email and already-member must return the same error code")
	assert.Equal(t, "add_member_failed", bodyUnknown["code"],
		"expected the generic add_member_failed code")
}
