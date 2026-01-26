package settlement

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type Settlement struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	GroupID   uuid.UUID       `json:"group_id" db:"group_id"`
	FromUser  uuid.UUID       `json:"from_user" db:"from_user"`
	ToUser    uuid.UUID       `json:"to_user" db:"to_user"`
	Amount    decimal.Decimal `json:"amount" db:"amount"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}

type CreateSettlementRequest struct {
	GroupID  uuid.UUID       `json:"group_id" validate:"required"`
	FromUser uuid.UUID       `json:"from_user" validate:"required"`
	ToUser   uuid.UUID       `json:"to_user" validate:"required"`
	Amount   decimal.Decimal `json:"amount" validate:"required,gt=0"`
}

func CreateSettlement(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	groupID := req.GroupID

	// Check if user is member of group
	var isMember bool
	err := db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, userID).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Check from_user and to_user are members
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, req.FromUser).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "from_user is not a member of the group"})
		return
	}
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, req.ToUser).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "to_user is not a member of the group"})
		return
	}

	if req.FromUser == req.ToUser {
		c.JSON(400, gin.H{"error": "cannot settle to self"})
		return
	}

	// Insert settlement
	var s Settlement
	err = db.Pool.QueryRow(c.Request.Context(),
		"INSERT INTO settlements (group_id, from_user, to_user, amount) VALUES ($1, $2, $3, $4) RETURNING id, group_id, from_user, to_user, amount, created_at",
		groupID, req.FromUser, req.ToUser, req.Amount).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create settlement"})
		return
	}

	c.JSON(201, s)
}
