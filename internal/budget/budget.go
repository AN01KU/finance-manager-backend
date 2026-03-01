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
	ID        uuid.UUID             `json:"id" db:"id"`
	UserID    uuid.UUID             `json:"user_id" db:"user_id"`
	Amount    helpers.StringDecimal `json:"amount" db:"amount"`
	Month     int                   `json:"month" db:"month"`
	Year      int                   `json:"year" db:"year"`
	CreatedAt time.Time             `json:"created_at" db:"created_at"`
	UpdatedAt time.Time             `json:"updated_at" db:"updated_at"`
}

type SetBudgetRequest struct {
	Amount string `json:"amount" validate:"required,numeric"`
	Month  int    `json:"month" validate:"required,min=1,max=12"`
	Year   int    `json:"year" validate:"required,min=2000,max=2100"`
}

// SetMonthlyBudget sets or updates the budget for a specific month
func SetMonthlyBudget(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req SetBudgetRequest
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

	if amount.LessThan(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount cannot be negative"})
		return
	}

	// Upsert budget
	var budget MonthlyBudget
	err = db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO monthly_budgets (user_id, amount, month, year, updated_at) 
		 VALUES ($1, $2, $3, $4, NOW()) 
		 ON CONFLICT (user_id, month, year) 
		 DO UPDATE SET amount = $2, updated_at = NOW()
		 RETURNING id, user_id, amount, month, year, created_at, updated_at`,
		userID, amount, req.Month, req.Year).Scan(
		&budget.ID, &budget.UserID, &budget.Amount, &budget.Month, &budget.Year,
		&budget.CreatedAt, &budget.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to set budget"})
		return
	}

	c.JSON(200, budget)
}

// GetMonthlyBudget retrieves the budget for a specific month
func GetMonthlyBudget(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	monthStr := c.Query("month")
	yearStr := c.Query("year")

	if monthStr == "" || yearStr == "" {
		// Default to current month
		now := time.Now()
		if monthStr == "" {
			monthStr = now.Format("1")
		}
		if yearStr == "" {
			yearStr = now.Format("2006")
		}
	}

	var month, year int
	if _, err := fmt.Sscanf(monthStr, "%d", &month); err != nil || month < 1 || month > 12 {
		c.JSON(400, gin.H{"error": "invalid month"})
		return
	}
	if _, err := fmt.Sscanf(yearStr, "%d", &year); err != nil || year < 2000 || year > 2100 {
		c.JSON(400, gin.H{"error": "invalid year"})
		return
	}

	var budget MonthlyBudget
	err := db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, amount, month, year, created_at, updated_at 
		 FROM monthly_budgets 
		 WHERE user_id = $1 AND month = $2 AND year = $3`,
		userID, month, year).Scan(
		&budget.ID, &budget.UserID, &budget.Amount, &budget.Month, &budget.Year,
		&budget.CreatedAt, &budget.UpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "budget not found for this month"})
		return
	}

	c.JSON(200, budget)
}

// ListBudgets retrieves all budgets for a user
func ListBudgets(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT id, user_id, amount, month, year, created_at, updated_at 
		 FROM monthly_budgets 
		 WHERE user_id = $1 
		 ORDER BY year DESC, month DESC`,
		userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve budgets"})
		return
	}
	defer rows.Close()

	var budgets []MonthlyBudget
	for rows.Next() {
		var budget MonthlyBudget
		if err := rows.Scan(&budget.ID, &budget.UserID, &budget.Amount, &budget.Month,
			&budget.Year, &budget.CreatedAt, &budget.UpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan budget"})
			return
		}
		budgets = append(budgets, budget)
	}

	if budgets == nil {
		budgets = []MonthlyBudget{}
	}

	c.JSON(200, budgets)
}
