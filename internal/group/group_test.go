package group

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/testutil"
)

func setupBalanceTestDB(t *testing.T) *db.DB {
	t.Helper()
	database := testutil.SetupDB(t)
	testutil.TruncateAll(t, database)
	return database
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

func TestBalanceCalculation(t *testing.T) {
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	userA := testutil.CreateUser(t, testDB, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, testDB, "b@example.com", "userb", "password")
	userC := testutil.CreateUser(t, testDB, "c@example.com", "userc", "password")

	groupID := testutil.CreateGroup(t, testDB, "Test Group", userA)
	testutil.AddGroupMember(t, testDB, groupID, userB)
	testutil.AddGroupMember(t, testDB, groupID, userC)

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
				testutil.CreateSettlement(t, testDB, groupID, userB, userA, decimal.NewFromFloat(20))
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
				testutil.CreateSettlement(t, testDB, groupID, userC, userA, decimal.NewFromFloat(10))
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

	creator := testutil.CreateUser(t, testDB, "creator-anti@example.com", "creator-anti", "password")
	already := testutil.CreateUser(t, testDB, "already-anti@example.com", "already-anti", "password")
	groupID := testutil.CreateGroup(t, testDB, "Test Group", creator)
	testutil.AddGroupMember(t, testDB, groupID, already)

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

// TestRemoveMember_NonCreatorMemberCanRemove verifies the symmetric
// permission model: any current member can remove any other current
// member (subject to the zero-balance precondition and the creator
// protection). Previously only the group creator could remove members,
// which deadlocks cleanup when the creator goes inactive.
func TestRemoveMember_NonCreatorMemberCanRemove(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	creator := testutil.CreateUser(t, testDB, "creator-rm@example.com", "creator-rm", "password")
	memberA := testutil.CreateUser(t, testDB, "membera-rm@example.com", "membera-rm", "password")
	memberB := testutil.CreateUser(t, testDB, "memberb-rm@example.com", "memberb-rm", "password")
	groupID := testutil.CreateGroup(t, testDB, "Test Group", creator)
	testutil.AddGroupMember(t, testDB, groupID, memberA)
	testutil.AddGroupMember(t, testDB, groupID, memberB)

	// memberA (NOT the creator) removes memberB.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", memberA)
	c.Params = gin.Params{
		{Key: "id", Value: groupID.String()},
		{Key: "userId", Value: memberB.String()},
	}
	c.Request = httptest.NewRequest("DELETE",
		"/groups/"+groupID.String()+"/members/"+memberB.String(), nil)
	RemoveMember(c, testDB)

	assert.Equal(t, 200, w.Code, "non-creator member must be allowed to remove another member; body=%s", w.Body.String())

	// And memberB is actually gone.
	stillMember, err := pgxBoolRow(t, testDB,
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2)`,
		groupID, memberB)
	require.NoError(t, err)
	assert.False(t, stillMember, "memberB should no longer be a member of the group")
}

// TestRemoveMember_CreatorStillCannotBeRemoved verifies the creator
// protection survives the symmetric-permission change — any member can
// remove anyone EXCEPT the creator.
func TestRemoveMember_CreatorStillCannotBeRemoved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	creator := testutil.CreateUser(t, testDB, "creator-rm2@example.com", "creator-rm2", "password")
	memberA := testutil.CreateUser(t, testDB, "membera-rm2@example.com", "membera-rm2", "password")
	groupID := testutil.CreateGroup(t, testDB, "Test Group", creator)
	testutil.AddGroupMember(t, testDB, groupID, memberA)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", memberA)
	c.Params = gin.Params{
		{Key: "id", Value: groupID.String()},
		{Key: "userId", Value: creator.String()},
	}
	c.Request = httptest.NewRequest("DELETE",
		"/groups/"+groupID.String()+"/members/"+creator.String(), nil)
	RemoveMember(c, testDB)

	assert.Equal(t, 400, w.Code,
		"removing the creator must still be rejected; body=%s", w.Body.String())
}

// TestRemoveMember_NonMemberCannotRemove verifies that callers who are
// not members of the group cannot remove anyone (closes a privilege
// hole that the symmetric-permission change would otherwise open).
func TestRemoveMember_NonMemberCannotRemove(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testDB := setupBalanceTestDB(t)
	defer testDB.Close()

	creator := testutil.CreateUser(t, testDB, "creator-rm3@example.com", "creator-rm3", "password")
	memberA := testutil.CreateUser(t, testDB, "membera-rm3@example.com", "membera-rm3", "password")
	outsider := testutil.CreateUser(t, testDB, "outsider-rm3@example.com", "outsider-rm3", "password")
	groupID := testutil.CreateGroup(t, testDB, "Test Group", creator)
	testutil.AddGroupMember(t, testDB, groupID, memberA)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", outsider)
	c.Params = gin.Params{
		{Key: "id", Value: groupID.String()},
		{Key: "userId", Value: memberA.String()},
	}
	c.Request = httptest.NewRequest("DELETE",
		"/groups/"+groupID.String()+"/members/"+memberA.String(), nil)
	RemoveMember(c, testDB)

	assert.Equal(t, 403, w.Code,
		"non-member callers must not be able to remove anyone")
}

// pgxBoolRow runs a SELECT EXISTS-style query and returns the boolean.
func pgxBoolRow(t *testing.T, testDB *db.DB, q string, args ...interface{}) (bool, error) {
	t.Helper()
	var b bool
	err := testDB.Pool.QueryRow(context.Background(), q, args...).Scan(&b)
	return b, err
}
