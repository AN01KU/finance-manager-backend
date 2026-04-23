package notify

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

var validate = validator.New()

type DeviceToken struct {
	ID        uuid.UUID           `json:"id"`
	UserID    uuid.UUID           `json:"user_id"`
	Token     string              `json:"token"`
	Platform  string              `json:"platform"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
	UpdatedAt helpers.EpochMillis `json:"updated_at"`
}

type RegisterTokenRequest struct {
	Token    string `json:"token" validate:"required"`
	Platform string `json:"platform" validate:"required,oneof=ios android web"`
}

type UnregisterTokenRequest struct {
	Token string `json:"token" validate:"required"`
}

// RegisterToken registers or updates a device push token for the authenticated user.
func RegisterToken(c *gin.Context, pool *pgxpool.Pool) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req RegisterTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var dt DeviceToken
	var rawCreatedAt, rawUpdatedAt time.Time
	err := pool.QueryRow(c.Request.Context(),
		`INSERT INTO device_tokens (user_id, token, platform)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, token) DO UPDATE SET
		   platform = EXCLUDED.platform,
		   updated_at = NOW()
		 RETURNING id, user_id, token, platform, created_at, updated_at`,
		userID, req.Token, req.Platform,
	).Scan(&dt.ID, &dt.UserID, &dt.Token, &dt.Platform, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to register device token"})
		return
	}
	dt.CreatedAt = helpers.FromTime(rawCreatedAt)
	dt.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, dt)
}

// UnregisterToken removes a device push token for the authenticated user.
func UnregisterToken(c *gin.Context, pool *pgxpool.Pool) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req UnregisterTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	tag, err := pool.Exec(c.Request.Context(),
		`DELETE FROM device_tokens WHERE user_id = $1 AND token = $2`,
		userID, req.Token,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to unregister device token"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(404, gin.H{"error": "device token not found"})
		return
	}

	c.JSON(200, gin.H{"message": "device token unregistered"})
}
