package settlement

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

var validate = validator.New()

type Settlement struct {
	ID        uuid.UUID             `json:"id"`
	GroupID   uuid.UUID             `json:"group_id"`
	FromUser  uuid.UUID             `json:"from_user"`
	ToUser    uuid.UUID             `json:"to_user"`
	Amount    float64               `json:"amount"`
	Notes     *string               `json:"notes,omitempty"`
	CreatedAt helpers.EpochMillis   `json:"created_at"`
}

type CreateSettlementRequest struct {
	GroupID  uuid.UUID `json:"group_id" validate:"required"`
	FromUser uuid.UUID `json:"from_user" validate:"required"`
	ToUser   uuid.UUID `json:"to_user" validate:"required"`
	Amount   float64   `json:"amount" validate:"required"`
	Notes    *string   `json:"notes,omitempty"`
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

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	groupID := req.GroupID

	// Check if user is member of group
	isMember, err := helpers.IsGroupMember(c.Request.Context(), db, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Check from_user and to_user are members
	isMember, err = helpers.IsGroupMember(c.Request.Context(), db, groupID, req.FromUser)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "from_user is not a member of the group"})
		return
	}
	isMember, err = helpers.IsGroupMember(c.Request.Context(), db, groupID, req.ToUser)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "to_user is not a member of the group"})
		return
	}

	if req.FromUser == req.ToUser {
		c.JSON(400, gin.H{"error": "cannot settle to self"})
		return
	}

	// The requesting user must be a party to the settlement
	if userID != req.FromUser && userID != req.ToUser {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	amount := decimal.NewFromFloat(req.Amount)

	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be greater than 0"})
		return
	}

	// Use a DB transaction so settlement + income transaction are atomic
	dbTx, err := db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer dbTx.Rollback(c.Request.Context())

	// Insert settlement
	var s Settlement
	var rawCreatedAt time.Time
	err = dbTx.QueryRow(c.Request.Context(),
		"INSERT INTO settlements (group_id, from_user, to_user, amount, notes) VALUES ($1, $2, $3, $4, $5) RETURNING id, group_id, from_user, to_user, amount, notes, created_at",
		groupID, req.FromUser, req.ToUser, amount, req.Notes).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &rawCreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create settlement"})
		return
	}
	s.CreatedAt = helpers.FromTime(rawCreatedAt)

	// Create income transaction for the to_user (they received money)
	_, err = dbTx.Exec(c.Request.Context(),
		`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
		 VALUES ($1, 'income', $2, 'Debt & Payments', NOW(), $3, $4, $5, $6)`,
		req.ToUser, amount, "Settlement received", req.Notes, groupID, s.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create income transaction for settlement"})
		return
	}

	// Create expense transaction for the from_user (they paid money)
	_, err = dbTx.Exec(c.Request.Context(),
		`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
		 VALUES ($1, 'expense', $2, 'Debt & Payments', NOW(), $3, $4, $5, $6)`,
		req.FromUser, amount, "Settlement paid", req.Notes, groupID, s.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create expense transaction for settlement"})
		return
	}

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(201, s)
}
