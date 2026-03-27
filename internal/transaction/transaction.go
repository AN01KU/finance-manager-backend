package transaction

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type Transaction struct {
	ID                 uuid.UUID             `json:"id"`
	UserID             uuid.UUID             `json:"user_id"`
	Type               string                `json:"type"`
	Amount             helpers.StringDecimal `json:"amount"`
	Category           string                `json:"category"`
	Date               time.Time             `json:"date"`
	Time               *time.Time            `json:"time,omitempty"`
	Description        *string               `json:"description,omitempty"`
	Notes              *string               `json:"notes,omitempty"`
	RecurringExpenseID *uuid.UUID            `json:"recurring_expense_id,omitempty"`
	GroupTransactionID *uuid.UUID            `json:"group_transaction_id,omitempty"`
	IsDeleted          bool                  `json:"is_deleted"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

type CreateTransactionRequest struct {
	ID                 *uuid.UUID `json:"id,omitempty"`
	Type               string     `json:"type" validate:"required,oneof=expense income"`
	Amount             string     `json:"amount" validate:"required,numeric"`
	Category           string     `json:"category" validate:"required,max=100"`
	Date               time.Time  `json:"date" validate:"required"`
	Time               *time.Time `json:"time,omitempty"`
	Description        *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes              *string    `json:"notes,omitempty"`
	RecurringExpenseID *uuid.UUID `json:"recurring_expense_id,omitempty"`
}

type UpdateTransactionRequest struct {
	Type        *string    `json:"type,omitempty" validate:"omitempty,oneof=expense income"`
	Amount      *string    `json:"amount,omitempty" validate:"omitempty,numeric"`
	Category    *string    `json:"category,omitempty" validate:"omitempty,max=100"`
	Date        *time.Time `json:"date,omitempty"`
	Time        *time.Time `json:"time,omitempty"`
	Description *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes       *string    `json:"notes,omitempty"`
}

func CreateTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid amount format"})
		return
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be greater than 0"})
		return
	}

	id := uuid.New()
	if req.ID != nil {
		id = *req.ID
	}

	var tx Transaction
	err = database.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO transactions (id, user_id, type, amount, category, date, time, description, notes, recurring_expense_id, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type,
		   amount = EXCLUDED.amount,
		   category = EXCLUDED.category,
		   date = EXCLUDED.date,
		   time = EXCLUDED.time,
		   description = EXCLUDED.description,
		   notes = EXCLUDED.notes,
		   recurring_expense_id = EXCLUDED.recurring_expense_id,
		   updated_at = NOW()
		 RETURNING id, user_id, type, amount, category, date, time, description, notes, recurring_expense_id, group_transaction_id, is_deleted, created_at, updated_at`,
		id, userID, req.Type, amount, req.Category, req.Date, req.Time,
		req.Description, req.Notes, req.RecurringExpenseID,
	).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category, &tx.Date, &tx.Time,
		&tx.Description, &tx.Notes, &tx.RecurringExpenseID, &tx.GroupTransactionID,
		&tx.IsDeleted, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create transaction"})
		return
	}

	c.JSON(201, tx)
}

func ListTransactions(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
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

	query := `SELECT id, user_id, type, amount, category, date, time, description, notes, recurring_expense_id, group_transaction_id, is_deleted, created_at, updated_at
		      FROM transactions WHERE user_id = $1`
	countQuery := `SELECT COUNT(*) FROM transactions WHERE user_id = $1`
	args := []interface{}{userID}
	n := 2

	// is_deleted filter
	if c.Query("is_deleted") == "true" {
		query += fmt.Sprintf(" AND is_deleted = $%d", n)
		countQuery += fmt.Sprintf(" AND is_deleted = $%d", n)
		args = append(args, true)
	} else {
		query += fmt.Sprintf(" AND is_deleted = $%d", n)
		countQuery += fmt.Sprintf(" AND is_deleted = $%d", n)
		args = append(args, false)
	}
	n++

	if v := c.Query("type"); v != "" {
		query += fmt.Sprintf(" AND type = $%d", n)
		countQuery += fmt.Sprintf(" AND type = $%d", n)
		args = append(args, v)
		n++
	}

	if v := c.Query("category"); v != "" {
		query += fmt.Sprintf(" AND category = $%d", n)
		countQuery += fmt.Sprintf(" AND category = $%d", n)
		args = append(args, v)
		n++
	}

	if v := c.Query("start_date"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			query += fmt.Sprintf(" AND date >= $%d", n)
			countQuery += fmt.Sprintf(" AND date >= $%d", n)
			args = append(args, t)
			n++
		}
	}

	if v := c.Query("end_date"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			query += fmt.Sprintf(" AND date <= $%d", n)
			countQuery += fmt.Sprintf(" AND date <= $%d", n)
			args = append(args, t)
			n++
		}
	}

	if v := c.Query("group_transaction_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			query += fmt.Sprintf(" AND group_transaction_id = $%d", n)
			countQuery += fmt.Sprintf(" AND group_transaction_id = $%d", n)
			args = append(args, id)
			n++
		}
	}

	if v := c.Query("recurring_expense_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			query += fmt.Sprintf(" AND recurring_expense_id = $%d", n)
			countQuery += fmt.Sprintf(" AND recurring_expense_id = $%d", n)
			args = append(args, id)
			n++
		}
	}

	var total int
	if err := database.Pool.QueryRow(c.Request.Context(), countQuery, args...).Scan(&total); err != nil {
		c.JSON(500, gin.H{"error": "failed to get total count"})
		return
	}

	query += fmt.Sprintf(" ORDER BY date DESC, created_at DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, limit, offset)

	rows, err := database.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve transactions"})
		return
	}
	defer rows.Close()

	txs := []Transaction{}
	for rows.Next() {
		var tx Transaction
		if err := rows.Scan(
			&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category, &tx.Date, &tx.Time,
			&tx.Description, &tx.Notes, &tx.RecurringExpenseID, &tx.GroupTransactionID,
			&tx.IsDeleted, &tx.CreatedAt, &tx.UpdatedAt,
		); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan transaction"})
			return
		}
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
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	var tx Transaction
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, type, amount, category, date, time, description, notes, recurring_expense_id, group_transaction_id, is_deleted, created_at, updated_at
		 FROM transactions WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category, &tx.Date, &tx.Time,
		&tx.Description, &tx.Notes, &tx.RecurringExpenseID, &tx.GroupTransactionID,
		&tx.IsDeleted, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if err != nil {
		c.JSON(404, gin.H{"error": "transaction not found"})
		return
	}

	c.JSON(200, tx)
}

func UpdateTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	// Group-linked transactions are managed via the group transaction endpoint
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

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var parsedAmount *decimal.Decimal
	if req.Amount != nil {
		a, err := decimal.NewFromString(*req.Amount)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid amount format"})
			return
		}
		if a.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be greater than 0"})
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
		args = append(args, *req.Date)
		n++
	}
	if req.Time != nil {
		query += fmt.Sprintf(", time = $%d", n)
		args = append(args, *req.Time)
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

	query += fmt.Sprintf(` WHERE id = $%d
		RETURNING id, user_id, type, amount, category, date, time, description, notes, recurring_expense_id, group_transaction_id, is_deleted, created_at, updated_at`, n)
	args = append(args, id)

	var tx Transaction
	err = database.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Category, &tx.Date, &tx.Time,
		&tx.Description, &tx.Notes, &tx.RecurringExpenseID, &tx.GroupTransactionID,
		&tx.IsDeleted, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update transaction"})
		return
	}

	c.JSON(200, tx)
}

func DeleteTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
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
		`UPDATE transactions SET is_deleted = true, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete transaction"})
		return
	}

	c.JSON(200, gin.H{"message": "transaction deleted successfully"})
}
