package transaction

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/recurring"
)

var validate = validator.New()

type Transaction struct {
	ID                 uuid.UUID              `json:"id"`
	UserID             uuid.UUID              `json:"user_id"`
	Type               string                 `json:"type"`
	Amount             float64                `json:"amount"`
	Category           string                 `json:"category"`
	Date               helpers.EpochMillis    `json:"date"`
	Description        *string                `json:"description,omitempty"`
	Notes              *string                `json:"notes,omitempty"`
	RecurringTransactionID *uuid.UUID             `json:"recurring_transaction_id,omitempty"`
	GroupTransactionID *uuid.UUID             `json:"group_transaction_id,omitempty"`
	GroupID            *uuid.UUID             `json:"group_id,omitempty"`
	GroupName          *string                `json:"group_name,omitempty"`
	SettlementID       *uuid.UUID             `json:"settlement_id,omitempty"`
	IsDeleted          bool                   `json:"is_deleted"`
	CreatedAt          helpers.EpochMillis    `json:"created_at"`
	UpdatedAt          helpers.EpochMillis    `json:"updated_at"`
}

type CreateTransactionRequest struct {
	ID                 *uuid.UUID `json:"id,omitempty"`
	Type               string     `json:"type" validate:"required,oneof=expense income"`
	Amount             float64    `json:"amount" validate:"required"`
	Category           string     `json:"category" validate:"required,max=100"`
	Date               int64      `json:"date" validate:"required"`
	Description        *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes              *string    `json:"notes,omitempty"`
	RecurringTransactionID *uuid.UUID `json:"recurring_transaction_id,omitempty"`
}

type UpdateTransactionRequest struct {
	Type        *string `json:"type,omitempty" validate:"omitempty,oneof=expense income"`
	Amount      *float64 `json:"amount,omitempty"`
	Category    *string `json:"category,omitempty" validate:"omitempty,max=100"`
	Date        *int64  `json:"date,omitempty"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes       *string `json:"notes,omitempty"`
}

func CreateTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	amount := decimal.NewFromFloat(req.Amount)
	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be a positive number"})
		return
	}

	date := time.UnixMilli(req.Date).UTC()

	id := uuid.New()
	if req.ID != nil {
		id = *req.ID
	}

	var tx Transaction
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time

	err := database.Pool.QueryRow(c.Request.Context(),
		`WITH ins AS (
		   INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, updated_at)
		   VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		   ON CONFLICT (id) DO UPDATE SET
		     type = EXCLUDED.type,
		     amount = EXCLUDED.amount,
		     category = EXCLUDED.category,
		     date = EXCLUDED.date,
		     description = EXCLUDED.description,
		     notes = EXCLUDED.notes,
		     recurring_transaction_id = EXCLUDED.recurring_transaction_id,
		     updated_at = NOW()
		   WHERE transactions.user_id = EXCLUDED.user_id
		   RETURNING id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, group_transaction_id, settlement_id, is_deleted, created_at, updated_at
		 )
		 SELECT ins.*, g.name AS group_name
		 FROM ins
		 LEFT JOIN group_transactions gt ON ins.group_transaction_id = gt.id
		 LEFT JOIN groups g ON gt.group_id = g.id`,
		id, userID, req.Type, amount, req.Category, date,
		req.Description, req.Notes, req.RecurringTransactionID,
	).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category,
		&rawDate, &tx.Description, &tx.Notes,
		&tx.RecurringTransactionID, &tx.GroupTransactionID, &tx.SettlementID,
		&tx.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
		&tx.GroupName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(409, gin.H{"error": "transaction ID conflict with another user"})
			return
		}
		log.Printf("[ERROR] CreateTransaction: %v", err)
		c.JSON(500, gin.H{"error": "failed to create transaction"})
		return
	}

	tx.Date = helpers.FromTime(rawDate)
	tx.CreatedAt = helpers.FromTime(rawCreatedAt)
	tx.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	// Keep recurring rule's last_added_date in sync so GenerateDueTransactions
	// doesn't create duplicates. GREATEST ensures out-of-order inserts never
	// roll the date backwards.
	if req.RecurringTransactionID != nil {
		_, _ = database.Pool.Exec(c.Request.Context(),
			`UPDATE recurring_transactions
			 SET last_added_date = GREATEST(COALESCE(last_added_date, '-infinity'::timestamptz), $1),
			     updated_at = NOW()
			 WHERE id = $2 AND user_id = $3`,
			date, *req.RecurringTransactionID, userID,
		)
	}

	c.JSON(201, tx)
}

func ListTransactions(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	if err := recurring.GenerateDueTransactions(c.Request.Context(), userID, database, time.Now()); err != nil {
		log.Printf("[WARN] GenerateDueTransactions: %v", err)
	}

	limit := 50
	if s := c.Query("limit"); s != "" {
		if l, err := strconv.Atoi(s); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if s := c.Query("offset"); s != "" {
		if o, err := strconv.Atoi(s); err == nil && o >= 0 {
			offset = o
		}
	}

	query := `SELECT t.id, t.user_id, t.type, t.amount, t.category, t.date, t.description, t.notes, t.recurring_transaction_id, t.group_transaction_id, COALESCE(t.group_id, gt.group_id), COALESCE(g1.name, g2.name), t.settlement_id, t.is_deleted, t.created_at, t.updated_at, COUNT(*) OVER() AS total_count
		      FROM transactions t
		      LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
		      LEFT JOIN groups g1 ON gt.group_id = g1.id
		      LEFT JOIN groups g2 ON t.group_id = g2.id
		      WHERE t.user_id = $1`
	args := []interface{}{userID}
	n := 2

	if c.Query("is_deleted") == "true" {
		query += fmt.Sprintf(" AND t.is_deleted = $%d", n)
		args = append(args, true)
	} else {
		query += fmt.Sprintf(" AND t.is_deleted = $%d", n)
		args = append(args, false)
	}
	n++

	if v := c.Query("type"); v != "" {
		query += fmt.Sprintf(" AND t.type = $%d", n)
		args = append(args, v)
		n++
	}

	if v := c.Query("category"); v != "" {
		query += fmt.Sprintf(" AND t.category = $%d", n)
		args = append(args, v)
		n++
	}

	// start_date / end_date accepted as epoch ms
	if v := c.Query("start_date"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			query += fmt.Sprintf(" AND t.date >= $%d", n)
			args = append(args, time.UnixMilli(ms).UTC())
			n++
		}
	}

	if v := c.Query("end_date"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			query += fmt.Sprintf(" AND t.date <= $%d", n)
			args = append(args, time.UnixMilli(ms).UTC())
			n++
		}
	}

	if v := c.Query("group_transaction_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			query += fmt.Sprintf(" AND t.group_transaction_id = $%d", n)
			args = append(args, id)
			n++
		}
	}

	if v := c.Query("recurring_transaction_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			query += fmt.Sprintf(" AND t.recurring_transaction_id = $%d", n)
			args = append(args, id)
			n++
		}
	}

	query += fmt.Sprintf(" ORDER BY t.date DESC, t.created_at DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, limit, offset)

	rows, err := database.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		log.Printf("[ERROR] ListTransactions query: %v", err)
		c.JSON(500, gin.H{"error": "failed to retrieve transactions"})
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	var total int
	for rows.Next() {
		var tx Transaction
		var rawDate, rawCreatedAt, rawUpdatedAt time.Time
		if err := rows.Scan(
			&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category,
			&rawDate, &tx.Description, &tx.Notes,
			&tx.RecurringTransactionID, &tx.GroupTransactionID, &tx.GroupID, &tx.GroupName, &tx.SettlementID,
			&tx.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
			&total,
		); err != nil {
			log.Printf("[ERROR] ListTransactions scan: %v", err)
			c.JSON(500, gin.H{"error": "failed to scan transaction"})
			return
		}
		tx.Date = helpers.FromTime(rawDate)
		tx.CreatedAt = helpers.FromTime(rawCreatedAt)
		tx.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		txs = append(txs, tx)
	}

	c.JSON(200, gin.H{
		"data": txs,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  total,
		},
	})
}

func GetTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	var tx Transaction
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time

	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT t.id, t.user_id, t.type, t.amount, t.category, t.date, t.description, t.notes, t.recurring_transaction_id, t.group_transaction_id, COALESCE(t.group_id, gt.group_id), COALESCE(g1.name, g2.name), t.settlement_id, t.is_deleted, t.created_at, t.updated_at
		 FROM transactions t
		 LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
		 LEFT JOIN groups g1 ON gt.group_id = g1.id
		 LEFT JOIN groups g2 ON t.group_id = g2.id
		 WHERE t.id = $1 AND t.user_id = $2 AND t.is_deleted = FALSE`,
		id, userID,
	).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category,
		&rawDate, &tx.Description, &tx.Notes,
		&tx.RecurringTransactionID, &tx.GroupTransactionID, &tx.GroupID, &tx.GroupName, &tx.SettlementID,
		&tx.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
	)
	if err != nil {
		c.JSON(404, gin.H{"error": "transaction not found"})
		return
	}

	tx.Date = helpers.FromTime(rawDate)
	tx.CreatedAt = helpers.FromTime(rawCreatedAt)
	tx.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, tx)
}

func UpdateTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	var ownerID uuid.UUID
	var groupTxID *uuid.UUID
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, group_transaction_id FROM transactions WHERE id = $1`, id,
	).Scan(&ownerID, &groupTxID)
	if err != nil {
		c.JSON(404, gin.H{"error": "transaction not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this transaction"})
		return
	}
	if groupTxID != nil {
		c.JSON(400, gin.H{"error": "group transactions must be updated via /groups/:id/transactions/:txId"})
		return
	}

	var req UpdateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var parsedAmount *decimal.Decimal
	if req.Amount != nil {
		a := decimal.NewFromFloat(*req.Amount)
		if a.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be a positive number"})
			return
		}
		parsedAmount = &a
	}

	query := `UPDATE transactions SET updated_at = NOW()`
	args := []interface{}{}
	n := 1

	if req.Type != nil {
		query += fmt.Sprintf(", type = $%d", n)
		args = append(args, *req.Type)
		n++
	}
	if parsedAmount != nil {
		query += fmt.Sprintf(", amount = $%d", n)
		args = append(args, parsedAmount)
		n++
	}
	if req.Category != nil {
		query += fmt.Sprintf(", category = $%d", n)
		args = append(args, *req.Category)
		n++
	}
	if req.Date != nil {
		query += fmt.Sprintf(", date = $%d", n)
		args = append(args, time.UnixMilli(*req.Date).UTC())
		n++
	}
	if req.Description != nil {
		query += fmt.Sprintf(", description = $%d", n)
		args = append(args, *req.Description)
		n++
	}
	if req.Notes != nil {
		query += fmt.Sprintf(", notes = $%d", n)
		args = append(args, *req.Notes)
		n++
	}

	if n == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(` WHERE id = $%d AND user_id = $%d
		RETURNING id, user_id, type, amount, category, date, description, notes, recurring_transaction_id, group_transaction_id, settlement_id, is_deleted, created_at, updated_at`, n, n+1)
	args = append(args, id, userID)

	var tx Transaction
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time

	// Wrap in CTE to join group name
	wrappedQuery := fmt.Sprintf(
		`WITH upd AS (%s)
		 SELECT upd.*, g.name AS group_name
		 FROM upd
		 LEFT JOIN group_transactions gt ON upd.group_transaction_id = gt.id
		 LEFT JOIN groups g ON gt.group_id = g.id`, query)

	err = database.Pool.QueryRow(c.Request.Context(), wrappedQuery, args...).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category,
		&rawDate, &tx.Description, &tx.Notes,
		&tx.RecurringTransactionID, &tx.GroupTransactionID, &tx.SettlementID,
		&tx.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
		&tx.GroupName,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update transaction"})
		return
	}

	tx.Date = helpers.FromTime(rawDate)
	tx.CreatedAt = helpers.FromTime(rawCreatedAt)
	tx.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, tx)
}

func DeleteTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	var ownerID uuid.UUID
	var groupTxID *uuid.UUID
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, group_transaction_id FROM transactions WHERE id = $1`, id,
	).Scan(&ownerID, &groupTxID)
	if err != nil {
		c.JSON(404, gin.H{"error": "transaction not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this transaction"})
		return
	}
	if groupTxID != nil {
		c.JSON(400, gin.H{"error": "group transactions must be deleted via /groups/:id/transactions/:txId"})
		return
	}

	_, err = database.Pool.Exec(c.Request.Context(),
		`UPDATE transactions SET is_deleted = true, updated_at = NOW() WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete transaction"})
		return
	}

	c.JSON(200, gin.H{"message": "transaction deleted successfully"})
}
