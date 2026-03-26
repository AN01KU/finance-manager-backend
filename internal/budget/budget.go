package budget

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

type MonthlyBudget struct {
	ID        uuid.UUID             `json:"id"`
	UserID    uuid.UUID             `json:"user_id"`
	Year      int                   `json:"year"`
	Month     int                   `json:"month"`
	Limit     helpers.StringDecimal `json:"limit"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type CreateBudgetRequest struct {
	ID    *uuid.UUID `json:"id,omitempty"`
	Limit string     `json:"limit" validate:"required,numeric"`
	Month int        `json:"month" validate:"required,min=1,max=12"`
	Year  int        `json:"year" validate:"required,min=2000,max=2100"`
}

type UpdateBudgetRequest struct {
	Limit *string `json:"limit" validate:"omitempty,numeric"`
	Month *int    `json:"month" validate:"omitempty,min=1,max=12"`
	Year  *int    `json:"year" validate:"omitempty,min=2000,max=2100"`
}

// CreateBudget creates or updates the budget for a specific month
func CreateBudget(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	amount, err := decimal.NewFromString(req.Limit)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid limit format"})
		return
	}

	if amount.LessThan(decimal.Zero) {
		c.JSON(400, gin.H{"error": "limit cannot be negative"})
		return
	}

	budgetID := uuid.New()
	if req.ID != nil {
		budgetID = *req.ID
	}

	var budget MonthlyBudget
	err = db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO monthly_budgets (id, user_id, budget_limit, month, year, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (user_id, month, year)
		 DO UPDATE SET budget_limit = EXCLUDED.budget_limit, updated_at = NOW()
		 RETURNING id, user_id, budget_limit, month, year, created_at, updated_at`,
		budgetID, userID, amount, req.Month, req.Year).Scan(
		&budget.ID, &budget.UserID, &budget.Limit, &budget.Month, &budget.Year,
		&budget.CreatedAt, &budget.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to set budget"})
		return
	}

	c.JSON(200, budget)
}

// ListBudgets retrieves budgets for a user with optional month/year filtering
func ListBudgets(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	monthStr := c.Query("month")
	yearStr := c.Query("year")

	query := `SELECT id, user_id, budget_limit, month, year, created_at, updated_at
		 FROM monthly_budgets
		 WHERE user_id = $1`
	args := []interface{}{userID}
	argCount := 2

	if monthStr != "" {
		var month int
		if _, err := fmt.Sscanf(monthStr, "%d", &month); err != nil || month < 1 || month > 12 {
			c.JSON(400, gin.H{"error": "invalid month"})
			return
		}
		query += fmt.Sprintf(" AND month = $%d", argCount)
		args = append(args, month)
		argCount++
	}

	if yearStr != "" {
		var year int
		if _, err := fmt.Sscanf(yearStr, "%d", &year); err != nil || year < 2000 || year > 2100 {
			c.JSON(400, gin.H{"error": "invalid year"})
			return
		}
		query += fmt.Sprintf(" AND year = $%d", argCount)
		args = append(args, year)
		argCount++
	}

	query += " ORDER BY year DESC, month DESC"

	rows, err := db.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve budgets"})
		return
	}
	defer rows.Close()

	budgets := []MonthlyBudget{}
	for rows.Next() {
		var budget MonthlyBudget
		if err := rows.Scan(&budget.ID, &budget.UserID, &budget.Limit, &budget.Month,
			&budget.Year, &budget.CreatedAt, &budget.UpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan budget"})
			return
		}
		budgets = append(budgets, budget)
	}

	c.JSON(200, gin.H{"data": budgets})
}

// UpdateBudget partially updates a budget by ID
func UpdateBudget(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	budgetIDStr := c.Param("id")
	budgetID, err := uuid.Parse(budgetIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid budget id"})
		return
	}

	var req UpdateBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Parse limit if provided
	var parsedLimit *decimal.Decimal
	if req.Limit != nil {
		limit, err := decimal.NewFromString(*req.Limit)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid limit format"})
			return
		}
		if limit.LessThan(decimal.Zero) {
			c.JSON(400, gin.H{"error": "limit cannot be negative"})
			return
		}
		parsedLimit = &limit
	}

	// Check ownership
	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM monthly_budgets WHERE id = $1`, budgetID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "budget not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this budget"})
		return
	}

	// Build update query dynamically
	query := `UPDATE monthly_budgets SET updated_at = NOW()`
	args := []interface{}{}
	argCount := 1

	if parsedLimit != nil {
		query += fmt.Sprintf(", budget_limit = $%d", argCount)
		args = append(args, parsedLimit)
		argCount++
	}
	if req.Month != nil {
		query += fmt.Sprintf(", month = $%d", argCount)
		args = append(args, *req.Month)
		argCount++
	}
	if req.Year != nil {
		query += fmt.Sprintf(", year = $%d", argCount)
		args = append(args, *req.Year)
		argCount++
	}

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, budget_limit, month, year, created_at, updated_at", argCount)
	args = append(args, budgetID)

	var budget MonthlyBudget
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&budget.ID, &budget.UserID, &budget.Limit, &budget.Month, &budget.Year,
		&budget.CreatedAt, &budget.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update budget"})
		return
	}

	c.JSON(200, budget)
}

// DeleteBudget deletes a budget by ID
func DeleteBudget(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	budgetIDStr := c.Param("id")
	budgetID, err := uuid.Parse(budgetIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid budget id"})
		return
	}

	// Check ownership
	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM monthly_budgets WHERE id = $1`, budgetID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "budget not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this budget"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM monthly_budgets WHERE id = $1`, budgetID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete budget"})
		return
	}

	c.JSON(200, gin.H{"message": "budget deleted successfully"})
}
