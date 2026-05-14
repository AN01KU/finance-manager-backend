package category

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

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/testutil"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	database := testutil.SetupDB(t)
	testutil.TruncateUsers(t, database)
	return database
}

func injectUserID(c *gin.Context, userID uuid.UUID) {
	c.Set("user_id", userID)
}

// callList calls GET /categories for the given user and returns the recorder.
func callList(t *testing.T, database *db.DB, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/categories", nil)
	injectUserID(c, userID)
	ListCategories(c, database)
	return w
}

// callCreate calls POST /categories and returns the recorder.
func callCreate(t *testing.T, database *db.DB, userID uuid.UUID, req CreateCategoryRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/categories", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	injectUserID(c, userID)
	CreateCategory(c, database)
	return w
}

// callUpdate calls PATCH /categories/:id and returns the recorder.
func callUpdate(t *testing.T, database *db.DB, userID uuid.UUID, catID uuid.UUID, req UpdateCategoryRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(req)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PATCH", "/categories/"+catID.String(), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: catID.String()}}
	injectUserID(c, userID)
	UpdateCategory(c, database)
	return w
}

// callDelete calls DELETE /categories/:id and returns the recorder.
func callDelete(t *testing.T, database *db.DB, userID uuid.UUID, catID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/categories/"+catID.String(), nil)
	c.Params = gin.Params{{Key: "id", Value: catID.String()}}
	injectUserID(c, userID)
	DeleteCategory(c, database)
	return w
}

// callGetPredefined calls GET /predefined-categories.
func callGetPredefined(t *testing.T, database *db.DB) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/predefined-categories", nil)
	GetPredefinedCategoriesHandler(c, database)
	return w
}

func parseListData(t *testing.T, w *httptest.ResponseRecorder) []Category {
	t.Helper()
	var resp struct {
		Data []Category `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data
}

func parsePredefinedData(t *testing.T, w *httptest.ResponseRecorder) []PredefinedCategory {
	t.Helper()
	var resp struct {
		Data []PredefinedCategory `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data
}

func parseSingleCategory(t *testing.T, w *httptest.ResponseRecorder) Category {
	t.Helper()
	var cat Category
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cat))
	return cat
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// TestGetPredefinedCategories_ReturnsMasterList verifies the open endpoint
// returns seeded predefined categories ordered by name, excluding hidden ones.
func TestGetPredefinedCategories_ReturnsMasterList(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	w := callGetPredefined(t, database)
	assert.Equal(t, 200, w.Code)

	cats := parsePredefinedData(t, w)
	assert.True(t, len(cats) > 0, "expected non-empty predefined list")

	// Verify the "other" predefined category is present
	var foundOther bool
	for _, c := range cats {
		assert.NotEmpty(t, c.Key)
		assert.NotEmpty(t, c.Name)
		assert.NotEmpty(t, c.Icon)
		assert.NotEmpty(t, c.Color)
		if c.Key == ProtectedKey {
			foundOther = true
		}
	}
	assert.True(t, foundOther, "expected 'other' category in predefined list")

	// Verify ordering is alphabetical by name
	for i := 1; i < len(cats); i++ {
		assert.True(t, cats[i-1].Name <= cats[i].Name, "predefined list should be sorted by name")
	}
}

// TestCreateCustomCategory_AppearsInList verifies a newly created custom
// category appears in GET /categories.
func TestCreateCustomCategory_AppearsInList(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	w := callCreate(t, database, userID, CreateCategoryRequest{
		Name:  strPtr("My Custom"),
		Icon:  strPtr("coffee-cafe"),
		Color: strPtr("#123456"),
	})
	require.Equal(t, 201, w.Code)

	cat := parseSingleCategory(t, w)
	assert.Equal(t, "My Custom", cat.Name)
	assert.Equal(t, "coffee-cafe", cat.Icon)
	assert.Equal(t, "#123456", cat.Color)
	assert.False(t, cat.IsPredefined)
	assert.Equal(t, userID, cat.UserID)

	// Verify it appears in the list
	listW := callList(t, database, userID)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	var found bool
	for _, item := range items {
		if item.ID == cat.ID {
			found = true
		}
	}
	assert.True(t, found, "custom category should appear in list after creation")
}

// TestCreatePredefinedOverride_ChangesNameInList verifies that creating a
// predefined override changes how the category appears.
func TestCreatePredefinedOverride_ChangesNameInList(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	w := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("food-dining"),
		Name:          strPtr("Eating Out"),
		Color:         strPtr("#ABCDEF"),
	})
	require.Equal(t, 201, w.Code)

	cat := parseSingleCategory(t, w)
	assert.Equal(t, "food-dining", cat.Key, "key should be the predefined key")
	assert.Equal(t, "Eating Out", cat.Name)
	assert.Equal(t, "#ABCDEF", cat.Color)
	assert.True(t, cat.IsPredefined)
	assert.Equal(t, userID, cat.UserID)

	// Verify it appears in list
	listW := callList(t, database, userID)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	var found bool
	for _, item := range items {
		if item.Key == "food-dining" {
			assert.Equal(t, "Eating Out", item.Name)
			found = true
		}
	}
	assert.True(t, found, "predefined override should appear in list")
}

// TestHidePredefinedCategory_ViaOverride verifies that setting is_hidden=true
// on a predefined override creates the row and marks it hidden.
func TestHidePredefinedCategory_ViaOverride(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	w := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("shopping"),
		IsHidden:      boolPtr(true),
	})
	require.Equal(t, 201, w.Code)

	cat := parseSingleCategory(t, w)
	assert.True(t, cat.IsHidden)
	assert.Equal(t, "shopping", cat.Key)

	// The override row appears in list with is_hidden=true
	listW := callList(t, database, userID)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	var found bool
	for _, item := range items {
		if item.Key == "shopping" {
			assert.True(t, item.IsHidden)
			found = true
		}
	}
	assert.True(t, found, "hidden override row should still appear in list for tombstone sync")
}

// TestDeleteCustomCategory_SoftDeletesAndDisappearsFromList verifies that
// deleting a custom category soft-deletes it and it no longer appears in the
// list.
func TestDeleteCustomCategory_SoftDeletesAndDisappearsFromList(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// Create a custom category
	createW := callCreate(t, database, userID, CreateCategoryRequest{
		Name:  strPtr("Temporary Cat"),
		Icon:  strPtr("atm-cash"),
		Color: strPtr("#000000"),
	})
	require.Equal(t, 201, createW.Code)
	cat := parseSingleCategory(t, createW)

	// Delete it
	delW := callDelete(t, database, userID, cat.ID)
	assert.Equal(t, 200, delW.Code)

	// Verify it no longer appears in the list
	listW := callList(t, database, userID)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	for _, item := range items {
		assert.NotEqual(t, cat.ID, item.ID, "soft-deleted category should not appear in list")
	}

	// Verify the row still physically exists (soft-delete, not hard delete)
	var isDeleted bool
	err := database.Pool.QueryRow(context.Background(),
		`SELECT is_deleted FROM custom_categories WHERE id = $1`, cat.ID).Scan(&isDeleted)
	require.NoError(t, err)
	assert.True(t, isDeleted, "row should be soft-deleted (is_deleted = TRUE)")
}

// TestDeletePredefinedOverride_SoftDeletesForTombstone verifies that deleting
// a predefined override soft-deletes it so clients get a tombstone for revert.
func TestDeletePredefinedOverride_SoftDeletesForTombstone(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// Create an override
	createW := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("groceries"),
		Name:          strPtr("Weekly Shop"),
	})
	require.Equal(t, 201, createW.Code)
	cat := parseSingleCategory(t, createW)

	// Delete the override
	delW := callDelete(t, database, userID, cat.ID)
	assert.Equal(t, 200, delW.Code)

	// Row must still exist with is_deleted=TRUE (tombstone for client sync)
	var isDeleted bool
	err := database.Pool.QueryRow(context.Background(),
		`SELECT is_deleted FROM custom_categories WHERE id = $1`, cat.ID).Scan(&isDeleted)
	require.NoError(t, err)
	assert.True(t, isDeleted, "predefined override should be soft-deleted as tombstone")

	// The deleted row should NOT appear in ListCategories (is_deleted filter)
	listW := callList(t, database, userID)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	for _, item := range items {
		assert.NotEqual(t, cat.ID, item.ID, "deleted override should not appear in active list")
	}
}

// TestDeleteOtherCategory_IsRejected verifies that attempting to delete (or
// hide via override) the protected "other" category is rejected. "other" is
// the catch-all bucket and cannot be hidden from users.
//
// The handler must reject any CreateCategory call that sets is_hidden=true for
// the protected key, and any DeleteCategory call on a custom category whose
// key is "other" (or an override whose predefined_key is "other").
func TestHideOtherCategory_IsRejected(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// Attempting to create a hidden override for "other" should be rejected.
	w := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr(ProtectedKey),
		IsHidden:      boolPtr(true),
	})
	assert.Equal(t, 400, w.Code, "creating a hidden override for 'other' should return 400")

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "error")
}

// TestCreateOverride_DuplicateReturns409 verifies that attempting to create a
// second override for the same predefined key returns 409.
func TestCreateOverride_DuplicateReturns409(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// First override — should succeed
	w1 := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("transport"),
		Name:          strPtr("Commute"),
	})
	require.Equal(t, 201, w1.Code)

	// Second override for same key — should conflict
	w2 := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("transport"),
		Name:          strPtr("Transit"),
	})
	assert.Equal(t, 409, w2.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	assert.Equal(t, "OVERRIDE_ALREADY_EXISTS", resp["code"])
}

// TestCreateOverride_UnknownKeyReturns404 verifies that a non-existent
// predefined key returns 404.
func TestCreateOverride_UnknownKeyReturns404(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	w := callCreate(t, database, userID, CreateCategoryRequest{
		PredefinedKey: strPtr("does-not-exist-key"),
		Name:          strPtr("Ghost"),
	})
	assert.Equal(t, 404, w.Code)
}

// TestUpdateCategory_VirtualPredefinedID_CreatesOverrideRow verifies that
// PATCHing a virtual predefined UUID (no DB row yet) creates an override row.
func TestUpdateCategory_VirtualPredefinedID_CreatesOverrideRow(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// Compute the virtual UUID for "entertainment"
	virtualID := virtualPredefinedID(userID, "entertainment")

	w := callUpdate(t, database, userID, virtualID, UpdateCategoryRequest{
		Name: strPtr("Fun & Games"),
	})
	require.Equal(t, 200, w.Code)

	cat := parseSingleCategory(t, w)
	assert.Equal(t, "entertainment", cat.Key)
	assert.Equal(t, "Fun & Games", cat.Name)
	assert.True(t, cat.IsPredefined)

	// The row now physically exists
	var count int
	err := database.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM custom_categories WHERE id = $1`, virtualID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "override row should exist after update via virtual ID")
}

// TestCategoryList_IsUserScoped verifies that user B's categories don't
// appear in user A's list.
func TestCategoryList_IsUserScoped(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userA := testutil.CreateUser(t, database, "a@test.com", "usera", "password")
	userB := testutil.CreateUser(t, database, "b@test.com", "userb", "password")

	// Create a category for user B
	createW := callCreate(t, database, userB, CreateCategoryRequest{
		Name:  strPtr("User B Cat"),
		Icon:  strPtr("gifts"),
		Color: strPtr("#FF0000"),
	})
	require.Equal(t, 201, createW.Code)
	catB := parseSingleCategory(t, createW)

	// User A's list should not contain user B's category
	listW := callList(t, database, userA)
	assert.Equal(t, 200, listW.Code)
	items := parseListData(t, listW)
	for _, item := range items {
		assert.NotEqual(t, catB.ID, item.ID, "user A should not see user B's category")
	}
}

// TestDeleteCustomCategory_CascadesTransactionsToOther verifies that
// deleting a custom category reassigns its transactions to "other".
func TestDeleteCustomCategory_CascadesTransactionsToOther(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "user@test.com", "testuser", "password")

	// Create a custom category
	createW := callCreate(t, database, userID, CreateCategoryRequest{
		Name:  strPtr("Hobby Spending"),
		Icon:  strPtr("gaming"),
		Color: strPtr("#654321"),
	})
	require.Equal(t, 201, createW.Code)
	cat := parseSingleCategory(t, createW)

	// Insert a personal transaction with this category key
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO transactions (user_id, type, amount, category, date, description)
		 VALUES ($1, 'expense', 100.00, $2, NOW(), 'Test tx')`,
		userID, cat.Key)
	require.NoError(t, err)

	// Delete the category
	delW := callDelete(t, database, userID, cat.ID)
	require.Equal(t, 200, delW.Code)

	// Verify the transaction was reassigned to "other"
	var txCategory string
	err = database.Pool.QueryRow(context.Background(),
		`SELECT category FROM transactions WHERE user_id = $1 AND description = 'Test tx'`,
		userID).Scan(&txCategory)
	require.NoError(t, err)
	assert.Equal(t, ProtectedKey, txCategory, "transaction category should be reassigned to 'other'")
}
