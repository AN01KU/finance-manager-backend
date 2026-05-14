package budget

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanonymousV2/finance-manager-backend/internal/dashboard"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/testutil"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database := testutil.SetupDB(t)
	testutil.TruncateAll(t, database)
	return database
}

func injectUserID(c *gin.Context, userID uuid.UUID) {
	c.Set("user_id", userID)
}

func callSetBudget(t *testing.T, database *db.DB, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PATCH", "/budgets", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	injectUserID(c, userID)
	SetBudget(c, database)
	return w
}

func callGetBudget(t *testing.T, database *db.DB, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/budgets", nil)
	injectUserID(c, userID)
	GetBudget(c, database)
	return w
}

func callGetDashboard(t *testing.T, database *db.DB, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/dashboard", nil)
	injectUserID(c, userID)
	dashboard.GetMonthlyDashboard(c, database)
	return w
}

func TestSetBudget_UpdatesUsersRow(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget1@example.com", "budgetuser1", "password123")

	w := callSetBudget(t, database, userID, `{"limit": 500.0}`)
	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.InDelta(t, 500.0, resp["limit"], 0.01)

	// Verify directly in DB that the scalar was written to users table
	var stored *float64
	err := database.Pool.QueryRow(context.Background(),
		`SELECT monthly_budget::float8 FROM users WHERE id = $1`, userID,
	).Scan(&stored)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.InDelta(t, 500.0, *stored, 0.01)
}

func TestGetBudget_ReturnsSetValue(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget2@example.com", "budgetuser2", "password123")

	callSetBudget(t, database, userID, `{"limit": 1000.0}`)

	w := callGetBudget(t, database, userID)
	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.InDelta(t, 1000.0, resp["limit"], 0.01)
}

func TestGetBudget_NullWhenUnset(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget3@example.com", "budgetuser3", "password123")

	w := callGetBudget(t, database, userID)
	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp["limit"])
}

func TestSetBudget_ClearWithNull(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget4@example.com", "budgetuser4", "password123")

	// Set a budget first
	callSetBudget(t, database, userID, `{"limit": 300.0}`)

	// Clear it
	w := callSetBudget(t, database, userID, `{"limit": null}`)
	assert.Equal(t, 200, w.Code)

	var setResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &setResp))
	assert.Nil(t, setResp["limit"])

	// Confirm GET also returns null
	w2 := callGetBudget(t, database, userID)
	var getResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &getResp))
	assert.Nil(t, getResp["limit"])
}

func TestDashboard_IncludesBudgetFields_WhenSet(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget5@example.com", "budgetuser5", "password123")
	callSetBudget(t, database, userID, `{"limit": 800.0}`)

	w := callGetDashboard(t, database, userID)
	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.NotNil(t, resp["budget"], "budget should be present when set")
	assert.InDelta(t, 800.0, resp["budget"], 0.01)

	require.NotNil(t, resp["remaining_budget"], "remaining_budget should be present when budget is set")
	// No expenses: remaining should equal budget
	assert.InDelta(t, 800.0, resp["remaining_budget"], 0.01)

	isOver, ok := resp["is_over_budget"].(bool)
	require.True(t, ok)
	assert.False(t, isOver)
}

func TestDashboard_BudgetFieldsAbsent_WhenNotSet(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "budget6@example.com", "budgetuser6", "password123")

	w := callGetDashboard(t, database, userID)
	assert.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// budget and remaining_budget use omitempty — should be absent
	_, hasBudget := resp["budget"]
	_, hasRemaining := resp["remaining_budget"]
	assert.False(t, hasBudget, "budget should be absent when not set")
	assert.False(t, hasRemaining, "remaining_budget should be absent when not set")

	isOver, ok := resp["is_over_budget"].(bool)
	require.True(t, ok)
	assert.False(t, isOver)
}

func TestBudget_UserScoped(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "budget7a@example.com", "budgetuser7a", "password123")
	userB := testutil.CreateUser(t, database, "budget7b@example.com", "budgetuser7b", "password123")

	callSetBudget(t, database, userA, `{"limit": 999.0}`)

	// User B should have no budget
	wB := callGetBudget(t, database, userB)
	var respB map[string]interface{}
	require.NoError(t, json.Unmarshal(wB.Body.Bytes(), &respB))
	assert.Nil(t, respB["limit"], "user B should not see user A's budget")

	// User B's dashboard should also have no budget fields
	wDB := callGetDashboard(t, database, userB)
	var dashB map[string]interface{}
	require.NoError(t, json.Unmarshal(wDB.Body.Bytes(), &dashB))
	_, hasBudget := dashB["budget"]
	assert.False(t, hasBudget, "user B dashboard should not include user A's budget")
}
