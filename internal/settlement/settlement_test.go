package settlement

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

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database := testutil.SetupDB(t)
	testutil.TruncateAll(t, database)
	return database
}

// injectUserID sets the user_id key in the Gin context, simulating a
// successfully authenticated request without running the JWT middleware.
func injectUserID(c *gin.Context, userID uuid.UUID) {
	c.Set("user_id", userID)
}

// createGroupTxWithSplits inserts a group transaction and its splits directly,
// bypassing the handler. Used to set up pairwise debt state for settlement tests.
func createGroupTxWithSplits(t *testing.T, database *db.DB, groupID, paidBy uuid.UUID, total decimal.Decimal, splits map[uuid.UUID]decimal.Decimal) {
	t.Helper()
	gtID := uuid.New()
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, description)
		 VALUES ($1, $2, $3, $4, 'other', NOW(), 'test expense')`,
		gtID, groupID, paidBy, total)
	require.NoError(t, err)

	for userID, amount := range splits {
		_, err := database.Pool.Exec(context.Background(),
			`INSERT INTO group_transaction_splits (group_transaction_id, user_id, amount) VALUES ($1, $2, $3)`,
			gtID, userID, amount)
		require.NoError(t, err)
	}
}

// countPersonalTxnsForSettlement returns the number of non-deleted personal
// transactions linked to a settlement row.
func countPersonalTxnsForSettlement(t *testing.T, database *db.DB, settlementID uuid.UUID) int {
	t.Helper()
	var count int
	err := database.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE settlement_id = $1 AND is_deleted = FALSE`,
		settlementID).Scan(&count)
	require.NoError(t, err)
	return count
}

// callCreate posts a CreateSettlementRequest through the handler and returns
// the recorder so the caller can assert on status and body.
func callCreate(t *testing.T, database *db.DB, callerID uuid.UUID, req CreateSettlementRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/settlements", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	injectUserID(c, callerID)

	CreateSettlement(c, database)
	return w
}

// callDelete sends a DELETE through the handler.
func callDelete(t *testing.T, database *db.DB, callerID, settlementID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/settlements/"+settlementID.String(), nil)
	c.Params = gin.Params{{Key: "id", Value: settlementID.String()}}
	injectUserID(c, callerID)

	DeleteSettlement(c, database)
	return w
}

// callUpdate sends a PATCH through the handler.
func callUpdate(t *testing.T, database *db.DB, callerID, settlementID uuid.UUID, req UpdateSettlementRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PATCH", "/settlements/"+settlementID.String(), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: settlementID.String()}}
	injectUserID(c, callerID)

	UpdateSettlement(c, database)
	return w
}

// parseSettlementID extracts the settlement ID from a 201 response body.
func parseSettlementID(t *testing.T, w *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	idStr, ok := resp["id"].(string)
	require.True(t, ok, "response missing id field")
	id, err := uuid.Parse(idStr)
	require.NoError(t, err)
	return id
}

// --- Tests ---

// TestCreateSettlement_PureDebtClearing verifies that when the settlement
// amount equals the pairwise debt (no excess), no personal transactions are
// created — the debt is fully cleared by the group transaction splits.
func TestCreateSettlement_PureDebtClearing(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// A paid 100, split equally: B owes A 50.
	createGroupTxWithSplits(t, database, groupID, userA, decimal.NewFromInt(100), map[uuid.UUID]decimal.Decimal{
		userA: decimal.NewFromInt(50),
		userB: decimal.NewFromInt(50),
	})

	// B settles exactly the debt amount → pure debt clearing, no excess.
	req := CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   50,
	}
	w := callCreate(t, database, userB, req)
	assert.Equal(t, 201, w.Code, w.Body.String())

	settlementID := parseSettlementID(t, w)
	assert.Equal(t, 0, countPersonalTxnsForSettlement(t, database, settlementID),
		"pure debt clearing must not create personal transactions")
}

// TestCreateSettlement_ExcessCreatesPersonalTxnPair verifies that when the
// settlement amount exceeds pairwise debt, a linked expense + income pair is
// created and the amounts match the excess portion.
func TestCreateSettlement_ExcessCreatesPersonalTxnPair(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// B owes A 50 from group expense.
	createGroupTxWithSplits(t, database, groupID, userA, decimal.NewFromInt(100), map[uuid.UUID]decimal.Decimal{
		userA: decimal.NewFromInt(50),
		userB: decimal.NewFromInt(50),
	})

	// B pays 80: 50 clears debt, 30 is excess.
	req := CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   80,
	}
	w := callCreate(t, database, userB, req)
	assert.Equal(t, 201, w.Code, w.Body.String())

	settlementID := parseSettlementID(t, w)
	assert.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID),
		"excess settlement must create an expense+income pair")

	// Verify the pair amounts equal the excess (30).
	var types []string
	var amounts []decimal.Decimal
	rows, err := database.Pool.Query(context.Background(),
		`SELECT type, amount FROM transactions WHERE settlement_id = $1 AND is_deleted = FALSE ORDER BY type`,
		settlementID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var typ string
		var amt decimal.Decimal
		require.NoError(t, rows.Scan(&typ, &amt))
		types = append(types, typ)
		amounts = append(amounts, amt)
	}
	assert.ElementsMatch(t, []string{"expense", "income"}, types)
	for _, amt := range amounts {
		assert.True(t, amt.Equal(decimal.NewFromInt(30)), "each linked txn must equal the excess (30), got %s", amt)
	}
}

// TestCreateSettlement_InvertedDirection verifies that when the settlement
// payer is actually the net creditor (recipient already owes payer), the entire
// settlement amount is treated as excess.
func TestCreateSettlement_InvertedDirection(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// A owes B 50 (B paid, A has the split).
	createGroupTxWithSplits(t, database, groupID, userB, decimal.NewFromInt(100), map[uuid.UUID]decimal.Decimal{
		userA: decimal.NewFromInt(50),
		userB: decimal.NewFromInt(50),
	})

	// Now A pays B 30 — but wait, A already owes B, so pairwiseDebt(B→A) is negative.
	// B is the creditor, and now B pays A 30 (inverted). Full 30 is excess.
	req := CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   30,
	}
	w := callCreate(t, database, userB, req)
	assert.Equal(t, 201, w.Code, w.Body.String())

	settlementID := parseSettlementID(t, w)
	assert.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID),
		"inverted direction must book full amount as excess (2 personal txns)")
}

// TestCreateSettlement_CrossCurrencyRejected verifies that a settlement between
// users with different currencies returns 400 with code MIXED_CURRENCY_SETTLEMENT.
func TestCreateSettlement_CrossCurrencyRejected(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// Give userB a different currency than userA (default is INR).
	_, err := database.Pool.Exec(context.Background(),
		`UPDATE users SET currency = 'USD' WHERE id = $1`, userB)
	require.NoError(t, err)

	req := CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   50,
	}
	w := callCreate(t, database, userB, req)
	assert.Equal(t, 400, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "MIXED_CURRENCY_SETTLEMENT", resp["code"])
}

// TestDeleteSettlement_SoftDeletesSettlementAndLinkedTxns verifies that
// deleting a settlement soft-deletes both the settlement row and any linked
// personal transactions (the 1-day undo window).
func TestDeleteSettlement_SoftDeletesSettlementAndLinkedTxns(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// Create excess settlement so linked personal txns exist.
	req := CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   30, // no prior debt → full amount is excess
	}
	w := callCreate(t, database, userB, req)
	require.Equal(t, 201, w.Code, w.Body.String())
	settlementID := parseSettlementID(t, w)

	require.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID))

	// Delete the settlement.
	w = callDelete(t, database, userB, settlementID)
	assert.Equal(t, 204, w.Code, w.Body.String())

	// Settlement row must be soft-deleted.
	var isDeleted bool
	err := database.Pool.QueryRow(context.Background(),
		`SELECT is_deleted FROM settlements WHERE id = $1`, settlementID).Scan(&isDeleted)
	require.NoError(t, err)
	assert.True(t, isDeleted, "settlement row must be soft-deleted")

	// Linked personal transactions must also be soft-deleted.
	assert.Equal(t, 0, countPersonalTxnsForSettlement(t, database, settlementID),
		"linked personal txns must be soft-deleted")

	// Confirm the rows still exist (soft-delete, not hard-delete).
	var totalLinked int
	err = database.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE settlement_id = $1`, settlementID).Scan(&totalLinked)
	require.NoError(t, err)
	assert.Equal(t, 2, totalLinked, "rows must exist (soft-deleted), not physically removed")
}

// TestUpdateSettlement_AmountChange_HardDeletesAndRecreatesTxns verifies that
// updating a settlement's amount hard-deletes the old linked transaction pair
// and recreates a new pair reflecting the updated excess.
func TestUpdateSettlement_AmountChange_HardDeletesAndRecreatesTxns(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	// Create settlement with excess so a linked pair exists.
	// No prior group debt → full 50 is excess.
	w := callCreate(t, database, userB, CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   50,
	})
	require.Equal(t, 201, w.Code, w.Body.String())
	settlementID := parseSettlementID(t, w)
	require.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID))

	// Update amount to 80 → new excess is 80.
	newAmt := 80.0
	w = callUpdate(t, database, userB, settlementID, UpdateSettlementRequest{Amount: &newAmt})
	assert.Equal(t, 200, w.Code, w.Body.String())

	// Old pair must be hard-deleted; new pair must exist with updated amounts.
	assert.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID),
		"new linked pair must exist after amount update")

	var newExcess decimal.Decimal
	err := database.Pool.QueryRow(context.Background(),
		`SELECT amount FROM transactions WHERE settlement_id = $1 AND is_deleted = FALSE LIMIT 1`,
		settlementID).Scan(&newExcess)
	require.NoError(t, err)
	assert.True(t, newExcess.Equal(decimal.NewFromInt(80)),
		"updated excess must reflect new settlement amount (80), got %s", newExcess)

	// Old rows must be physically gone (hard-deleted, not soft-deleted).
	var total int
	err = database.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE settlement_id = $1`, settlementID).Scan(&total)
	require.NoError(t, err)
	assert.Equal(t, 2, total, "hard-delete must leave exactly 2 rows (the new pair), not 4")
}

// TestUpdateSettlement_NotesOnly_DoesNotTouchTxns verifies that updating only
// the notes field on a settlement propagates the change to linked transactions
// without altering the personal transaction count or amounts.
func TestUpdateSettlement_NotesOnly_DoesNotTouchTxns(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@example.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@example.com", "userb", "password")
	groupID := testutil.CreateGroup(t, database, "test group", userA)
	testutil.AddGroupMember(t, database, groupID, userB)

	note := "initial note"
	w := callCreate(t, database, userB, CreateSettlementRequest{
		GroupID:  groupID,
		FromUser: userB,
		ToUser:   userA,
		Amount:   30,
		Notes:    &note,
	})
	require.Equal(t, 201, w.Code, w.Body.String())
	settlementID := parseSettlementID(t, w)
	require.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID))

	updatedNote := "updated note"
	w = callUpdate(t, database, userB, settlementID, UpdateSettlementRequest{Notes: &updatedNote})
	assert.Equal(t, 200, w.Code, w.Body.String())

	// Count must be unchanged.
	assert.Equal(t, 2, countPersonalTxnsForSettlement(t, database, settlementID))

	// Notes on linked transactions must be updated.
	var count int
	err := database.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE settlement_id = $1 AND notes = $2 AND is_deleted = FALSE`,
		settlementID, updatedNote).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "notes must be synced to both linked transactions")
}
