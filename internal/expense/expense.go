package expense

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

type Expense struct {
	ID                 uuid.UUID            `json:"id" db:"id"`
	UserID             uuid.UUID            `json:"user_id" db:"user_id"`
	Amount             helpers.StringDecimal `json:"amount" db:"amount"`
	Category           string               `json:"category" db:"category"`
	Date               time.Time            `json:"date" db:"date"`
	Time               *time.Time           `json:"time,omitempty" db:"time"`
	Description        *string              `json:"description,omitempty" db:"description"`
	Notes              *string              `json:"notes,omitempty" db:"notes"`
	CreatedAt          time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at" db:"updated_at"`
	IsDeleted          bool                 `json:"is_deleted" db:"is_deleted"`
	RecurringExpenseID *uuid.UUID           `json:"recurring_expense_id,omitempty" db:"recurring_expense_id"`
	GroupID            *uuid.UUID           `json:"group_id,omitempty" db:"group_id"`
	GroupName          *string              `json:"group_name,omitempty" db:"group_name"`
}

type CreateExpenseRequest struct {
	ID                 *uuid.UUID `json:"id,omitempty"`
	Amount             string     `json:"amount" validate:"required,numeric"`
	Category           string     `json:"category" validate:"required,max=50"`
	Date               time.Time  `json:"date" validate:"required"`
	Time               *time.Time `json:"time,omitempty"`
	Description        *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes              *string    `json:"notes,omitempty"`
	RecurringExpenseID *uuid.UUID `json:"recurring_expense_id,omitempty"`
	GroupID            *uuid.UUID `json:"group_id,omitempty"`
	GroupName          *string    `json:"group_name,omitempty"`
}

type UpdateExpenseRequest struct {
	Amount             *string    `json:"amount,omitempty" validate:"omitempty,numeric"`
	Category           *string    `json:"category,omitempty" validate:"omitempty,max=50"`
	Date               *time.Time `json:"date,omitempty"`
	Time               *time.Time `json:"time,omitempty"`
	Description        *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes              *string    `json:"notes,omitempty"`
	RecurringExpenseID *uuid.UUID `json:"recurring_expense_id,omitempty"`
	GroupID            *uuid.UUID `json:"group_id,omitempty"`
	GroupName          *string    `json:"group_name,omitempty"`
}

func CreateExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateExpenseRequest
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

	expenseID := uuid.New()
	if req.ID != nil {
		expenseID = *req.ID
	}

	var expense Expense
	err = db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO expenses (id, user_id, amount, category, date, time, description, notes, recurring_expense_id, group_id, group_name, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   amount = EXCLUDED.amount,
		   category = EXCLUDED.category,
		   date = EXCLUDED.date,
		   time = EXCLUDED.time,
		   description = EXCLUDED.description,
		   notes = EXCLUDED.notes,
		   recurring_expense_id = EXCLUDED.recurring_expense_id,
		   group_id = EXCLUDED.group_id,
		   group_name = EXCLUDED.group_name,
		   updated_at = NOW()
		 RETURNING id, user_id, amount, category, date, time, description, notes, created_at, updated_at, is_deleted, recurring_expense_id, group_id, group_name`,
		expenseID, userID, amount, req.Category, req.Date, req.Time, req.Description, req.Notes, req.RecurringExpenseID, req.GroupID, req.GroupName).Scan(
		&expense.ID, &expense.UserID, &expense.Amount, &expense.Category, &expense.Date, &expense.Time,
		&expense.Description, &expense.Notes, &expense.CreatedAt, &expense.UpdatedAt, &expense.IsDeleted,
		&expense.RecurringExpenseID, &expense.GroupID, &expense.GroupName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create expense"})
		return
	}

	c.JSON(201, expense)
}

func ListExpenses(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	limit := 50
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	query := `SELECT id, user_id, amount, category, date, time, description, notes, created_at, updated_at, is_deleted, recurring_expense_id, group_id, group_name
		      FROM expenses
		      WHERE user_id = $1`
	countQuery := `SELECT COUNT(*) FROM expenses WHERE user_id = $1`
	args := []interface{}{userID}
	argCount := 2

	// Default: filter out deleted unless explicitly requested
	if isDeletedStr := c.Query("is_deleted"); isDeletedStr == "true" {
		query += fmt.Sprintf(" AND is_deleted = $%d", argCount)
		countQuery += fmt.Sprintf(" AND is_deleted = $%d", argCount)
		args = append(args, true)
		argCount++
	} else {
		query += fmt.Sprintf(" AND is_deleted = $%d", argCount)
		countQuery += fmt.Sprintf(" AND is_deleted = $%d", argCount)
		args = append(args, false)
		argCount++
	}

	if categoryStr := c.Query("category"); categoryStr != "" {
		query += fmt.Sprintf(" AND category = $%d", argCount)
		countQuery += fmt.Sprintf(" AND category = $%d", argCount)
		args = append(args, categoryStr)
		argCount++
	}

	if startDateStr := c.Query("start_date"); startDateStr != "" {
		if startDate, err := time.Parse("2006-01-02", startDateStr); err == nil {
			query += fmt.Sprintf(" AND date >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND date >= $%d", argCount)
			args = append(args, startDate)
			argCount++
		}
	}

	if endDateStr := c.Query("end_date"); endDateStr != "" {
		if endDate, err := time.Parse("2006-01-02", endDateStr); err == nil {
			endDate = endDate.Add(24 * time.Hour)
			query += fmt.Sprintf(" AND date < $%d", argCount)
			countQuery += fmt.Sprintf(" AND date < $%d", argCount)
			args = append(args, endDate)
			argCount++
		}
	}

	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		if groupID, err := uuid.Parse(groupIDStr); err == nil {
			query += fmt.Sprintf(" AND group_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND group_id = $%d", argCount)
			args = append(args, groupID)
			argCount++
		}
	}

	if recurringIDStr := c.Query("recurring_expense_id"); recurringIDStr != "" {
		if recurringID, err := uuid.Parse(recurringIDStr); err == nil {
			query += fmt.Sprintf(" AND recurring_expense_id = $%d", argCount)
			countQuery += fmt.Sprintf(" AND recurring_expense_id = $%d", argCount)
			args = append(args, recurringID)
			argCount++
		}
	}

	var totalCount int
	if err := db.Pool.QueryRow(c.Request.Context(), countQuery, args...).Scan(&totalCount); err != nil {
		c.JSON(500, gin.H{"error": "failed to get total count"})
		return
	}

	query += fmt.Sprintf(" ORDER BY date DESC, created_at DESC LIMIT $%d OFFSET $%d", argCount, argCount+1)
	args = append(args, limit, offset)

	rows, err := db.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve expenses"})
		return
	}
	defer rows.Close()

	var expenses []Expense
	for rows.Next() {
		var exp Expense
		if err := rows.Scan(&exp.ID, &exp.UserID, &exp.Amount, &exp.Category, &exp.Date, &exp.Time,
			&exp.Description, &exp.Notes, &exp.CreatedAt, &exp.UpdatedAt, &exp.IsDeleted,
			&exp.RecurringExpenseID, &exp.GroupID, &exp.GroupName); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		expenses = append(expenses, exp)
	}

	if expenses == nil {
		expenses = []Expense{}
	}

	c.JSON(200, gin.H{
		"data": expenses,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  totalCount,
		},
	})
}

func GetExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	expenseIDStr := c.Param("id")
	expenseID, err := uuid.Parse(expenseIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid expense id"})
		return
	}

	var expense Expense
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, amount, category, date, time, description, notes, created_at, updated_at, is_deleted, recurring_expense_id, group_id, group_name
		 FROM expenses
		 WHERE id = $1 AND user_id = $2`,
		expenseID, userID).Scan(&expense.ID, &expense.UserID, &expense.Amount, &expense.Category,
		&expense.Date, &expense.Time, &expense.Description, &expense.Notes, &expense.CreatedAt,
		&expense.UpdatedAt, &expense.IsDeleted, &expense.RecurringExpenseID, &expense.GroupID, &expense.GroupName)
	if err != nil {
		c.JSON(404, gin.H{"error": "expense not found"})
		return
	}

	c.JSON(200, expense)
}

func UpdateExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	expenseIDStr := c.Param("id")
	expenseID, err := uuid.Parse(expenseIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid expense id"})
		return
	}

	var req UpdateExpenseRequest
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
		amount, err := decimal.NewFromString(*req.Amount)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid amount format"})
			return
		}
		if amount.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be greater than 0"})
			return
		}
		parsedAmount = &amount
	}

	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM expenses WHERE id = $1`, expenseID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this expense"})
		return
	}

	query := `UPDATE expenses SET updated_at = NOW()`
	args := []interface{}{}
	argCount := 1

	if parsedAmount != nil {
		query += fmt.Sprintf(", amount = $%d", argCount)
		args = append(args, parsedAmount)
		argCount++
	}
	if req.Category != nil {
		query += fmt.Sprintf(", category = $%d", argCount)
		args = append(args, req.Category)
		argCount++
	}
	if req.Date != nil {
		query += fmt.Sprintf(", date = $%d", argCount)
		args = append(args, req.Date)
		argCount++
	}
	if req.Time != nil {
		query += fmt.Sprintf(", time = $%d", argCount)
		args = append(args, req.Time)
		argCount++
	}
	if req.Description != nil {
		query += fmt.Sprintf(", description = $%d", argCount)
		args = append(args, req.Description)
		argCount++
	}
	if req.Notes != nil {
		query += fmt.Sprintf(", notes = $%d", argCount)
		args = append(args, req.Notes)
		argCount++
	}
	if req.RecurringExpenseID != nil {
		query += fmt.Sprintf(", recurring_expense_id = $%d", argCount)
		args = append(args, req.RecurringExpenseID)
		argCount++
	}
	if req.GroupID != nil {
		query += fmt.Sprintf(", group_id = $%d", argCount)
		args = append(args, req.GroupID)
		argCount++
	}
	if req.GroupName != nil {
		query += fmt.Sprintf(", group_name = $%d", argCount)
		args = append(args, req.GroupName)
		argCount++
	}

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, amount, category, date, time, description, notes, created_at, updated_at, is_deleted, recurring_expense_id, group_id, group_name", argCount)
	args = append(args, expenseID)

	var expense Expense
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&expense.ID, &expense.UserID, &expense.Amount, &expense.Category, &expense.Date, &expense.Time,
		&expense.Description, &expense.Notes, &expense.CreatedAt, &expense.UpdatedAt, &expense.IsDeleted,
		&expense.RecurringExpenseID, &expense.GroupID, &expense.GroupName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update expense"})
		return
	}

	c.JSON(200, expense)
}

func DeleteExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	expenseIDStr := c.Param("id")
	expenseID, err := uuid.Parse(expenseIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid expense id"})
		return
	}

	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM expenses WHERE id = $1`, expenseID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this expense"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`UPDATE expenses SET is_deleted = true, updated_at = NOW() WHERE id = $1`, expenseID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete expense"})
		return
	}

	c.JSON(200, gin.H{"message": "expense deleted successfully"})
}
