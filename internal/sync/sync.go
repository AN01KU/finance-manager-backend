package sync

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

type PreflightRequest struct {
	SyncSessionID uuid.UUID `json:"sync_session_id" validate:"required"`
}

type PreflightResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// ValidateSession checks if a sync session is active and belongs to the given user.
// Returns a reason string if invalid, or empty string if valid.
func ValidateSession(c *gin.Context, database *db.DB, syncSessionID uuid.UUID, jwtUserID uuid.UUID) (valid bool, reason string) {
	var sessionUserID uuid.UUID
	var invalidatedAt *string

	err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, invalidated_at FROM sync_sessions WHERE id = $1`,
		syncSessionID,
	).Scan(&sessionUserID, &invalidatedAt)

	if err == pgx.ErrNoRows {
		return false, "SYNC_SESSION_NOT_FOUND"
	}
	if err != nil {
		return false, "SYNC_SESSION_NOT_FOUND"
	}
	if invalidatedAt != nil {
		return false, "SYNC_SESSION_EXPIRED"
	}
	if sessionUserID != jwtUserID {
		return false, "SYNC_SESSION_MISMATCH"
	}

	return true, ""
}

// SyncSessionGuard returns a middleware that validates the optional X-Sync-Session-ID header.
// If the header is present, the session is validated against the JWT user.
// If the header is absent, the request proceeds as normal (backwards compatible).
func SyncSessionGuard(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionHeader := c.GetHeader("X-Sync-Session-ID")
		if sessionHeader == "" {
			c.Next()
			return
		}

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

		valid, reason := ValidateSession(c, database, syncSessionID, jwtUserID)
		if !valid {
			c.JSON(409, PreflightResponse{Valid: false, Reason: reason})
			c.Abort()
			return
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

	var req PreflightRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	valid, reason := ValidateSession(c, database, req.SyncSessionID, jwtUserID)
	if !valid {
		c.JSON(409, PreflightResponse{Valid: false, Reason: reason})
		return
	}

	// Update last_seen_at
	_, _ = database.Pool.Exec(c.Request.Context(),
		`UPDATE sync_sessions SET last_seen_at = now() WHERE id = $1`,
		req.SyncSessionID,
	)

	c.JSON(200, PreflightResponse{Valid: true})
}
