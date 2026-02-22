package personalexpense

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type PersonalExpense struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	Category    *string         `json:"category,omitempty" db:"category"`
	Amount      decimal.Decimal `json:"amount" db:"amount"`
	Description *string         `json:"description,omitempty" db:"description"`
	Notes       *string         `json:"notes,omitempty" db:"notes"`
	ExpenseDate time.Time       `json:"expense_date" db:"expense_date"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

type CreateExpenseRequest struct {
	Category    *string   `json:"category,omitempty" validate:"omitempty,max=50"`
	Amount      string    `json:"amount" validate:"required,numeric"`
	Description *string   `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes       *string   `json:"notes,omitempty"`
	ExpenseDate time.Time `json:"expense_date" validate:"required"`
}

type UpdateExpenseRequest struct {
	Category    *string    `json:"category,omitempty" validate:"omitempty,max=50"`
	Amount      *string    `json:"amount,omitempty" validate:"omitempty,numeric"`
	Description *string    `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes       *string    `json:"notes,omitempty"`
	ExpenseDate *time.Time `json:"expense_date,omitempty"`
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

	// Parse amount from string to decimal
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid amount format"})
		return
	}

	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be greater than 0"})
		return
	}

	var expense PersonalExpense
	err = db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO personal_expenses (user_id, category, amount, description, notes, expense_date, updated_at) 
		 VALUES ($1, $2, $3, $4, $5, $6, NOW()) 
		 RETURNING id, user_id, category, amount, description, notes, expense_date, created_at, updated_at`,
		userID, req.Category, amount, req.Description, req.Notes, req.ExpenseDate).Scan(
		&expense.ID, &expense.UserID, &expense.Category, &expense.Amount, &expense.Description,
		&expense.Notes, &expense.ExpenseDate, &expense.CreatedAt, &expense.UpdatedAt)
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

	query := `SELECT id, user_id, category_id, amount, description, notes, expense_date, created_at, updated_at 
		      FROM personal_expenses 
		      WHERE user_id = $1`
	countQuery := `SELECT COUNT(*) FROM personal_expenses WHERE user_id = $1`
	args := []interface{}{userID}
	argCount := 2

	if categoryStr := c.Query("category"); categoryStr != "" {
		query += fmt.Sprintf(" AND category = $%d", argCount)
		countQuery += fmt.Sprintf(" AND category = $%d", argCount)
		args = append(args, categoryStr)
		argCount++
	}

	if startDateStr := c.Query("start_date"); startDateStr != "" {
		if startDate, err := time.Parse("2006-01-02", startDateStr); err == nil {
			query += fmt.Sprintf(" AND expense_date >= $%d", argCount)
			countQuery += fmt.Sprintf(" AND expense_date >= $%d", argCount)
			args = append(args, startDate)
			argCount++
		}
	}

	if endDateStr := c.Query("end_date"); endDateStr != "" {
		if endDate, err := time.Parse("2006-01-02", endDateStr); err == nil {
			endDate = endDate.Add(24 * time.Hour)
			query += fmt.Sprintf(" AND expense_date < $%d", argCount)
			countQuery += fmt.Sprintf(" AND expense_date < $%d", argCount)
			args = append(args, endDate)
			argCount++
		}
	}

	var totalCount int
	if err := db.Pool.QueryRow(c.Request.Context(), countQuery, args...).Scan(&totalCount); err != nil {
		c.JSON(500, gin.H{"error": "failed to get total count"})
		return
	}

	query += fmt.Sprintf(" ORDER BY expense_date DESC, created_at DESC LIMIT $%d OFFSET $%d", argCount, argCount+1)
	args = append(args, limit, offset)

	rows, err := db.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve expenses"})
		return
	}
	defer rows.Close()

	var expenses []PersonalExpense
	for rows.Next() {
		var exp PersonalExpense
		if err := rows.Scan(&exp.ID, &exp.UserID, &exp.Category, &exp.Amount, &exp.Description,
			&exp.Notes, &exp.ExpenseDate, &exp.CreatedAt, &exp.UpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		expenses = append(expenses, exp)
	}

	if expenses == nil {
		expenses = []PersonalExpense{}
	}

	c.JSON(200, gin.H{
		"expenses": expenses,
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

	var expense PersonalExpense
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, category, amount, description, notes, expense_date, created_at, updated_at 
		 FROM personal_expenses 
		 WHERE id = $1 AND user_id = $2`,
		expenseID, userID).Scan(&expense.ID, &expense.UserID, &expense.Category, &expense.Amount,
		&expense.Description, &expense.Notes, &expense.ExpenseDate, &expense.CreatedAt, &expense.UpdatedAt)
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

	// Parse amount if provided
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
		`SELECT user_id FROM personal_expenses WHERE id = $1`, expenseID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this expense"})
		return
	}

	query := `UPDATE personal_expenses SET updated_at = NOW()`
	args := []interface{}{}
	argCount := 1

	if req.Category != nil {
		query += fmt.Sprintf(", category = $%d", argCount)
		args = append(args, req.Category)
		argCount++
	}
	if parsedAmount != nil {
		query += fmt.Sprintf(", amount = $%d", argCount)
		args = append(args, parsedAmount)
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
	if req.ExpenseDate != nil {
		query += fmt.Sprintf(", expense_date = $%d", argCount)
		args = append(args, req.ExpenseDate)
		argCount++
	}

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, category, amount, description, notes, expense_date, created_at, updated_at", argCount)
	args = append(args, expenseID)

	var expense PersonalExpense
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&expense.ID, &expense.UserID, &expense.Category, &expense.Amount, &expense.Description,
		&expense.Notes, &expense.ExpenseDate, &expense.CreatedAt, &expense.UpdatedAt)
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
		`SELECT user_id FROM personal_expenses WHERE id = $1`, expenseID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this expense"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM personal_expenses WHERE id = $1`, expenseID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete expense"})
		return
	}

	c.JSON(200, gin.H{"message": "expense deleted successfully"})
}
