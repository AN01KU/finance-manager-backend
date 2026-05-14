package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// createSyncSession inserts an active sync session for userID and returns its ID.
func createSyncSession(t *testing.T, database *db.DB, userID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := database.Pool.Exec(context.Background(),
		`INSERT INTO sync_sessions (id, user_id) VALUES ($1, $2)`,
		id, userID)
	require.NoError(t, err)
	return id
}

// expireSyncSession sets invalidated_at on a session to simulate expiry.
func expireSyncSession(t *testing.T, database *db.DB, sessionID uuid.UUID) {
	t.Helper()
	_, err := database.Pool.Exec(context.Background(),
		`UPDATE sync_sessions SET invalidated_at = $1, invalidation_reason = 'test_expiry' WHERE id = $2`,
		time.Now(), sessionID)
	require.NoError(t, err)
}

// newGinContext builds a fake *gin.Context backed by an httptest.ResponseRecorder.
func newGinContext(method, path string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	c.Request = req
	return c, w
}

// ─────────────────────────── ValidateSession tests ────────────────────────────

// TestValidateSession_HappyPath verifies valid=true and no reason for an active
// session belonging to the correct user.
func TestValidateSession_HappyPath(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "sync_happy@example.com", "syncuser", "pass")
	sessionID := createSyncSession(t, database, userID)

	c, _ := newGinContext(http.MethodPost, "/sync/preflight", nil)
	valid, reason, transient := ValidateSession(c, database, sessionID, userID)

	assert.True(t, valid)
	assert.Empty(t, reason)
	assert.False(t, transient)
}

// TestValidateSession_NotFound verifies that a completely unknown session ID
// returns false with ReasonNotFound (not a transient failure).
func TestValidateSession_NotFound(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "sync_notfound@example.com", "syncnf", "pass")
	unknownSessionID := uuid.New()

	c, _ := newGinContext(http.MethodPost, "/sync/preflight", nil)
	valid, reason, transient := ValidateSession(c, database, unknownSessionID, userID)

	assert.False(t, valid)
	assert.Equal(t, ReasonNotFound, reason)
	assert.False(t, transient)
}

// TestValidateSession_Expired verifies that a session with invalidated_at set
// returns false with ReasonExpired.
func TestValidateSession_Expired(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "sync_expired@example.com", "syncexp", "pass")
	sessionID := createSyncSession(t, database, userID)
	expireSyncSession(t, database, sessionID)

	c, _ := newGinContext(http.MethodPost, "/sync/preflight", nil)
	valid, reason, transient := ValidateSession(c, database, sessionID, userID)

	assert.False(t, valid)
	assert.Equal(t, ReasonExpired, reason)
	assert.False(t, transient)
}

// TestValidateSession_Mismatch verifies that a session belonging to a different
// user returns false with ReasonMismatch (even though the JWT is valid).
func TestValidateSession_Mismatch(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	ownerID := testutil.CreateUser(t, database, "sync_owner@example.com", "syncown", "pass")
	otherID := testutil.CreateUser(t, database, "sync_other@example.com", "syncoth", "pass")
	sessionID := createSyncSession(t, database, ownerID)

	c, _ := newGinContext(http.MethodPost, "/sync/preflight", nil)
	// otherID presents ownerID's session — should be rejected.
	valid, reason, transient := ValidateSession(c, database, sessionID, otherID)

	assert.False(t, valid)
	assert.Equal(t, ReasonMismatch, reason)
	assert.False(t, transient)
}

// TestValidateSession_Transient verifies that a closed pool returns transient=true
// and ReasonTransient (not a 409-class reason).
func TestValidateSession_Transient(t *testing.T) {
	database := setupTestDB(t)
	// Close the pool before calling ValidateSession to simulate a DB outage.
	database.Close()

	sessionID := uuid.New()
	userID := uuid.New()

	c, _ := newGinContext(http.MethodPost, "/sync/preflight", nil)
	valid, reason, transient := ValidateSession(c, database, sessionID, userID)

	assert.False(t, valid)
	assert.Equal(t, ReasonTransient, reason)
	assert.True(t, transient)
}

// ─────────────────────────── SyncSessionGuard tests ──────────────────────────

// guardRouter builds a minimal gin.Engine with the SyncSessionGuard on POST /test.
// A GET /test route has no guard to model the read-only bypass invariant.
func guardRouter(database *db.DB, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Inject user_id, simulating what JWT middleware does on authenticated routes.
	injectUser := func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Next()
	}

	sentinel := func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) }

	r.POST("/test", injectUser, SyncSessionGuard(database), sentinel)
	r.GET("/test", injectUser, sentinel) // no guard — mirrors read-only routes
	return r
}

// doGuardRequest sends a request through the guard router and returns the recorder.
func doGuardRequest(r *gin.Engine, method, sessionID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/test", nil)
	if sessionID != "" {
		req.Header.Set("X-Sync-Session-ID", sessionID)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestSyncSessionGuard_HappyPath checks that a valid session passes the guard
// and last_seen_at is refreshed.
func TestSyncSessionGuard_HappyPath(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "guard_ok@example.com", "guardok", "pass")
	sessionID := createSyncSession(t, database, userID)

	var before time.Time
	err := database.Pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM sync_sessions WHERE id = $1`, sessionID).Scan(&before)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	w := doGuardRequest(guardRouter(database, userID), http.MethodPost, sessionID.String())
	assert.Equal(t, 200, w.Code)

	var after time.Time
	err = database.Pool.QueryRow(context.Background(),
		`SELECT last_seen_at FROM sync_sessions WHERE id = $1`, sessionID).Scan(&after)
	require.NoError(t, err)
	assert.True(t, after.After(before), "last_seen_at should be refreshed after a valid guard pass")
}

// TestSyncSessionGuard_MissingHeader verifies 400 when the header is absent.
func TestSyncSessionGuard_MissingHeader(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "guard_nf_h@example.com", "guardnfh", "pass")
	w := doGuardRequest(guardRouter(database, userID), http.MethodPost, "")
	assert.Equal(t, 400, w.Code)
}

// TestSyncSessionGuard_NotFound verifies 409 with SYNC_SESSION_NOT_FOUND.
func TestSyncSessionGuard_NotFound(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "guard_nf@example.com", "guardnf", "pass")
	w := doGuardRequest(guardRouter(database, userID), http.MethodPost, uuid.New().String())
	assert.Equal(t, 409, w.Code)

	var resp PreflightResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ReasonNotFound, resp.Reason)
}

// TestSyncSessionGuard_Expired verifies 409 with SYNC_SESSION_EXPIRED.
func TestSyncSessionGuard_Expired(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "guard_exp@example.com", "guardexp", "pass")
	sessionID := createSyncSession(t, database, userID)
	expireSyncSession(t, database, sessionID)

	w := doGuardRequest(guardRouter(database, userID), http.MethodPost, sessionID.String())
	assert.Equal(t, 409, w.Code)

	var resp PreflightResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ReasonExpired, resp.Reason)
}

// TestSyncSessionGuard_Mismatch verifies 409 with SYNC_SESSION_MISMATCH when a
// valid JWT user presents another user's session.
func TestSyncSessionGuard_Mismatch(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	ownerID := testutil.CreateUser(t, database, "guard_own@example.com", "guardown", "pass")
	otherID := testutil.CreateUser(t, database, "guard_oth@example.com", "guardoth", "pass")
	sessionID := createSyncSession(t, database, ownerID)

	// JWT user is otherID but session belongs to ownerID.
	w := doGuardRequest(guardRouter(database, otherID), http.MethodPost, sessionID.String())
	assert.Equal(t, 409, w.Code)

	var resp PreflightResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ReasonMismatch, resp.Reason)
}

// TestSyncSessionGuard_Transient verifies 502 (not 409) when the DB pool is closed.
func TestSyncSessionGuard_Transient(t *testing.T) {
	database := setupTestDB(t)
	userID := testutil.CreateUser(t, database, "guard_tr@example.com", "guardtr", "pass")
	r := guardRouter(database, userID)
	// Close pool after building router so the guard hits a dead pool.
	database.Close()

	w := doGuardRequest(r, http.MethodPost, uuid.New().String())
	assert.Equal(t, 502, w.Code)

	var resp PreflightResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, ReasonTransient, resp.Reason)
}

// TestSyncSessionGuard_GetRequestBypassesGuard verifies the architectural invariant:
// GET routes have no guard in their chain, so an expired session does not block reads.
func TestSyncSessionGuard_GetRequestBypassesGuard(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()

	userID := testutil.CreateUser(t, database, "guard_get@example.com", "guardget", "pass")
	sessionID := createSyncSession(t, database, userID)
	expireSyncSession(t, database, sessionID)

	// GET /test has no SyncSessionGuard — expired session in header is irrelevant.
	w := doGuardRequest(guardRouter(database, userID), http.MethodGet, sessionID.String())
	assert.Equal(t, 200, w.Code)
}
