package settlement

import (
	"fmt"
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
	ID        uuid.UUID           `json:"id"`
	GroupID   uuid.UUID           `json:"group_id"`
	FromUser  uuid.UUID           `json:"from_user"`
	ToUser    uuid.UUID           `json:"to_user"`
	Amount    float64             `json:"amount"`
	Notes     *string             `json:"notes,omitempty"`
	IsDeleted bool                `json:"is_deleted"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
	UpdatedAt helpers.EpochMillis `json:"updated_at"`
}

type UpdateSettlementRequest struct {
	Amount *float64 `json:"amount,omitempty"`
	Notes  *string  `json:"notes,omitempty"`
}

type CreateSettlementRequest struct {
	GroupID  uuid.UUID `json:"group_id" validate:"required"`
	FromUser uuid.UUID `json:"from_user" validate:"required"`
	ToUser   uuid.UUID `json:"to_user" validate:"required"`
	Amount   float64   `json:"amount" validate:"required"`
	Notes    *string   `json:"notes,omitempty"`
}

func CreateSettlement(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	defer func() { _ = dbTx.Rollback(c.Request.Context()) }()

	// Insert settlement
	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = dbTx.QueryRow(c.Request.Context(),
		"INSERT INTO settlements (group_id, from_user, to_user, amount, notes) VALUES ($1, $2, $3, $4, $5) RETURNING id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at",
		groupID, req.FromUser, req.ToUser, amount, req.Notes).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create settlement"})
		return
	}
	s.CreatedAt = helpers.FromTime(rawCreatedAt)
	s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

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

func GetSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	// Only parties to the settlement or group members can view it
	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, s.GroupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	s.CreatedAt = helpers.FromTime(rawCreatedAt)
	s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, s)
}

func DeleteSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	if userID != s.FromUser && userID != s.ToUser {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer func() { _ = dbTx.Rollback(c.Request.Context()) }()

	_, err = dbTx.Exec(c.Request.Context(),
		`UPDATE transactions SET is_deleted = true, updated_at = NOW() WHERE settlement_id = $1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete linked transactions"})
		return
	}

	_, err = dbTx.Exec(c.Request.Context(),
		`UPDATE settlements SET is_deleted = true, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete settlement"})
		return
	}

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.Status(204)
}

func UpdateSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	if userID != s.FromUser && userID != s.ToUser {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	var req UpdateSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	settleQuery := `UPDATE settlements SET updated_at = NOW()`
	args := []interface{}{}
	n := 1

	txQuery := `UPDATE transactions SET updated_at = NOW()`
	var txArgs []interface{}
	tn := 1

	if req.Amount != nil {
		amount := decimal.NewFromFloat(*req.Amount)
		if amount.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be greater than 0"})
			return
		}
		settleQuery += fmt.Sprintf(", amount = $%d", n)
		args = append(args, amount)
		n++
		txQuery += fmt.Sprintf(", amount = $%d", tn)
		txArgs = append(txArgs, amount)
		tn++
	}
	if req.Notes != nil {
		settleQuery += fmt.Sprintf(", notes = $%d", n)
		args = append(args, *req.Notes)
		n++
		txQuery += fmt.Sprintf(", notes = $%d", tn)
		txArgs = append(txArgs, *req.Notes)
		tn++
	}

	if n == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	settleQuery += fmt.Sprintf(` WHERE id = $%d
		RETURNING id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at`, n)
	args = append(args, id)

	txQuery += fmt.Sprintf(` WHERE settlement_id = $%d AND is_deleted = FALSE`, tn)
	txArgs = append(txArgs, id)

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer func() { _ = dbTx.Rollback(c.Request.Context()) }()

	err = dbTx.QueryRow(c.Request.Context(), settleQuery, args...).Scan(
		&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update settlement"})
		return
	}
	s.CreatedAt = helpers.FromTime(rawCreatedAt)
	s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	if _, err = dbTx.Exec(c.Request.Context(), txQuery, txArgs...); err != nil {
		c.JSON(500, gin.H{"error": "failed to sync linked transactions"})
		return
	}

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(200, s)
}
