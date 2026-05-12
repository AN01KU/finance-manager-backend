package sync

import (
	"errors"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

const currentSyncVersion = "1"

// Reason codes returned to clients on sync validation failures. Distinct codes
// so the client can take the right action (retry vs orphan-and-reauth).
const (
	ReasonNotFound  = "SYNC_SESSION_NOT_FOUND"
	ReasonExpired   = "SYNC_SESSION_EXPIRED"
	ReasonMismatch  = "SYNC_SESSION_MISMATCH"
	ReasonTransient = "SYNC_TRANSIENT" // DB blip, pool exhausted; client should retry
)

func checkSyncVersion(c *gin.Context) {
	version := c.GetHeader("X-Sync-Version")
	if version == "" {
		log.Printf("[WARN] X-Sync-Version header missing, path=%s", c.Request.URL.Path)
		return
	}
	if version != currentSyncVersion {
		log.Printf("[WARN] X-Sync-Version unrecognised, version=%s path=%s", version, c.Request.URL.Path)
	}
}

type PreflightRequest struct {
	SyncSessionID uuid.UUID `json:"sync_session_id" validate:"required"`
}

type PreflightResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// ValidateSession checks whether a sync session is active and belongs to the
// given JWT user.
//
// Return values:
//   - valid:     true only when the session exists, is not expired, and matches
//     the JWT user.
//   - reason:    one of the SYNC_* codes when valid=false.
//   - transient: true when the failure is a DB-level issue (blip / pool /
//     deadline) that should NOT cause the client to orphan its
//     queue. When transient is true, callers should respond 502 so
//     the client retries.
func ValidateSession(c *gin.Context, database *db.DB, syncSessionID uuid.UUID, jwtUserID uuid.UUID) (valid bool, reason string, transient bool) {
	var sessionUserID uuid.UUID
	var invalidatedAt *time.Time
	var createdAt time.Time

	err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, invalidated_at, created_at FROM sync_sessions WHERE id = $1`,
		syncSessionID,
	).Scan(&sessionUserID, &invalidatedAt, &createdAt)

	if errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[SYNC] sync_session_not_found session_id=%s", syncSessionID)
		return false, ReasonNotFound, false
	}
	if err != nil {
		// Transient DB error — caller should let the client retry rather than
		// trash its offline queue.
		log.Printf("[SYNC] sync_session_transient session_id=%s err=%v", syncSessionID, err)
		return false, ReasonTransient, true
	}
	if invalidatedAt != nil {
		ageDays := int(time.Since(createdAt).Hours() / 24)
		log.Printf("[SYNC] sync_session_expired session_id=%s session_age_days=%d", syncSessionID, ageDays)
		return false, ReasonExpired, false
	}
	if sessionUserID != jwtUserID {
		log.Printf("[SYNC] sync_session_mismatch jwt_user_id=%s session_user_id=%s session_id=%s", jwtUserID, sessionUserID, syncSessionID)
		return false, ReasonMismatch, false
	}

	log.Printf("[SYNC] sync_preflight_ok session_id=%s user_id=%s", syncSessionID, jwtUserID)
	return true, "", false
}

// SyncSessionGuard returns middleware that REQUIRES a valid X-Sync-Session-ID
// header on every mutating request. There is no opt-out: a missing header is
// a 400, an invalid header is a 400, and a stale/foreign session is a 409.
//
// On successful validation, last_seen_at is refreshed so an actively-syncing
// client never gets expired by the TTL cleanup just because it doesn't call
// preflight on every batch.
//
// **Refresh is write-only:** only routes that flow through SyncSessionGuard
// (i.e. mutating endpoints) bump last_seen_at. Read-only requests under
// JWTAuth alone never touch the column. This is intentional for a
// write-heavy app like ours — a user who is actively recording transactions
// keeps their session alive without needing a separate heartbeat. A user
// who is purely browsing does not, and will eventually expire by idle TTL.
// If pure-reader sessions ever need keep-alive, add a refresh hook to
// /sync/preflight (already done) or to JWTAuth.
func SyncSessionGuard(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionHeader := c.GetHeader("X-Sync-Session-ID")
		if sessionHeader == "" {
			c.JSON(400, gin.H{"error": "X-Sync-Session-ID header is required"})
			c.Abort()
			return
		}

		checkSyncVersion(c)

		syncSessionID, err := uuid.Parse(sessionHeader)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid X-Sync-Session-ID header"})
			c.Abort()
			return
		}

		val, exists := c.Get("user_id")
		if !exists {
			c.JSON(401, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		jwtUserID, ok := val.(uuid.UUID)
		if !ok {
			c.JSON(401, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		valid, reason, transient := ValidateSession(c, database, syncSessionID, jwtUserID)
		if !valid {
			if transient {
				// 502 → the client should back off and retry; do NOT trash queue.
				c.JSON(502, PreflightResponse{Valid: false, Reason: reason})
			} else {
				c.JSON(409, PreflightResponse{Valid: false, Reason: reason})
			}
			c.Abort()
			return
		}

		// Refresh last_seen_at so an actively-syncing client doesn't get
		// expired by the TTL job just because they never call preflight.
		// Best-effort: log and proceed on failure.
		if _, err := database.Pool.Exec(c.Request.Context(),
			`UPDATE sync_sessions SET last_seen_at = now() WHERE id = $1`,
			syncSessionID,
		); err != nil {
			log.Printf("[SYNC] failed to refresh last_seen_at session_id=%s err=%v", syncSessionID, err)
		}

		c.Next()
	}
}

func Preflight(c *gin.Context, database *db.DB) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	jwtUserID, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	checkSyncVersion(c)

	var req PreflightRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	valid, reason, transient := ValidateSession(c, database, req.SyncSessionID, jwtUserID)
	if !valid {
		if transient {
			c.JSON(502, PreflightResponse{Valid: false, Reason: reason})
		} else {
			c.JSON(409, PreflightResponse{Valid: false, Reason: reason})
		}
		return
	}

	// Update last_seen_at
	_, _ = database.Pool.Exec(c.Request.Context(),
		`UPDATE sync_sessions SET last_seen_at = now() WHERE id = $1`,
		req.SyncSessionID,
	)

	c.JSON(200, PreflightResponse{Valid: true})
}
