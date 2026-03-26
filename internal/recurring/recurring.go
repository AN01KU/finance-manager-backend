package recurring

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

type RecurringExpense struct {
	ID            uuid.UUID             `json:"id"`
	UserID        uuid.UUID             `json:"user_id"`
	Name          string                `json:"name"`
	Amount        helpers.StringDecimal `json:"amount"`
	Category      string                `json:"category"`
	Frequency     string                `json:"frequency"`
	DayOfMonth    *int                  `json:"day_of_month,omitempty"`
	DaysOfWeek    []int                 `json:"days_of_week,omitempty"`
	StartDate     time.Time             `json:"start_date"`
	EndDate       *time.Time            `json:"end_date,omitempty"`
	IsActive      bool                  `json:"is_active"`
	LastAddedDate *time.Time            `json:"last_added_date,omitempty"`
	Notes         *string               `json:"notes,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type CreateRecurringExpenseRequest struct {
	ID         *uuid.UUID `json:"id,omitempty"`
	Name       string     `json:"name" validate:"required"`
	Amount     string     `json:"amount" validate:"required,numeric"`
	Category   string     `json:"category" validate:"required"`
	Frequency  string     `json:"frequency" validate:"required,oneof=daily weekly monthly yearly"`
	DayOfMonth *int       `json:"day_of_month,omitempty" validate:"omitempty,min=1,max=31"`
	DaysOfWeek []int      `json:"days_of_week,omitempty" validate:"omitempty,dive,min=0,max=6"`
	StartDate  time.Time  `json:"start_date" validate:"required"`
	EndDate    *time.Time `json:"end_date,omitempty"`
	Notes      *string    `json:"notes,omitempty"`
}

type UpdateRecurringExpenseRequest struct {
	Name       *string    `json:"name,omitempty" validate:"omitempty"`
	Amount     *string    `json:"amount,omitempty" validate:"omitempty,numeric"`
	Category   *string    `json:"category,omitempty" validate:"omitempty"`
	Frequency  *string    `json:"frequency,omitempty" validate:"omitempty,oneof=daily weekly monthly yearly"`
	DayOfMonth *int       `json:"day_of_month,omitempty" validate:"omitempty,min=1,max=31"`
	DaysOfWeek []int      `json:"days_of_week,omitempty" validate:"omitempty,dive,min=0,max=6"`
	StartDate  *time.Time `json:"start_date,omitempty"`
	EndDate    *time.Time `json:"end_date,omitempty"`
	IsActive   *bool      `json:"is_active,omitempty"`
	Notes      *string    `json:"notes,omitempty"`
}

func CreateRecurringExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateRecurringExpenseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Frequency == "weekly" && len(req.DaysOfWeek) == 0 {
		c.JSON(400, gin.H{"error": "days_of_week is required for weekly frequency"})
		return
	}
	if req.Frequency == "monthly" {
		if req.DayOfMonth == nil {
			c.JSON(400, gin.H{"error": "day_of_month is required for monthly frequency"})
			return
		}
		if *req.DayOfMonth < 1 || *req.DayOfMonth > 31 {
			c.JSON(400, gin.H{"error": "day_of_month must be between 1 and 31"})
			return
		}
	}

	if req.EndDate != nil && req.EndDate.Before(req.StartDate) {
		c.JSON(400, gin.H{"error": "end_date must be after start_date"})
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

	recurringID := uuid.New()
	if req.ID != nil {
		recurringID = *req.ID
	}

	var expense RecurringExpense
	err = db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO recurring_expenses (id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, notes, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, $11, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   amount = EXCLUDED.amount,
		   category = EXCLUDED.category,
		   frequency = EXCLUDED.frequency,
		   day_of_month = EXCLUDED.day_of_month,
		   days_of_week = EXCLUDED.days_of_week,
		   start_date = EXCLUDED.start_date,
		   end_date = EXCLUDED.end_date,
		   notes = EXCLUDED.notes,
		   updated_at = NOW()
		 RETURNING id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at`,
		recurringID, userID, req.Name, amount, req.Category, req.Frequency, req.DayOfMonth, req.DaysOfWeek, req.StartDate, req.EndDate, req.Notes).Scan(
		&expense.ID, &expense.UserID, &expense.Name, &expense.Amount, &expense.Category,
		&expense.Frequency, &expense.DayOfMonth, &expense.DaysOfWeek, &expense.StartDate,
		&expense.EndDate, &expense.IsActive, &expense.LastAddedDate, &expense.Notes, &expense.CreatedAt, &expense.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create recurring expense"})
		return
	}

	c.JSON(201, expense)
}

func ListRecurringExpenses(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	query := `SELECT id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at
		      FROM recurring_expenses
		      WHERE user_id = $1`
	args := []interface{}{userID}

	if activeStr := c.Query("active"); activeStr == "true" {
		query += " AND is_active = true"
	}

	query += " ORDER BY created_at DESC"

	rows, err := db.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve recurring expenses"})
		return
	}
	defer rows.Close()

	var expenses []RecurringExpense
	for rows.Next() {
		var exp RecurringExpense
		if err := rows.Scan(&exp.ID, &exp.UserID, &exp.Name, &exp.Amount, &exp.Category,
			&exp.Frequency, &exp.DayOfMonth, &exp.DaysOfWeek, &exp.StartDate,
			&exp.EndDate, &exp.IsActive, &exp.LastAddedDate, &exp.Notes, &exp.CreatedAt, &exp.UpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan recurring expense"})
			return
		}
		expenses = append(expenses, exp)
	}

	if expenses == nil {
		expenses = []RecurringExpense{}
	}

	c.JSON(200, gin.H{"data": expenses})
}

func GetRecurringExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring expense id"})
		return
	}

	var expense RecurringExpense
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at
		 FROM recurring_expenses
		 WHERE id = $1 AND user_id = $2`,
		id, userID).Scan(&expense.ID, &expense.UserID, &expense.Name, &expense.Amount, &expense.Category,
		&expense.Frequency, &expense.DayOfMonth, &expense.DaysOfWeek, &expense.StartDate,
		&expense.EndDate, &expense.IsActive, &expense.LastAddedDate, &expense.Notes, &expense.CreatedAt, &expense.UpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring expense not found"})
		return
	}

	c.JSON(200, expense)
}

func UpdateRecurringExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring expense id"})
		return
	}

	var req UpdateRecurringExpenseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Frequency != nil {
		if *req.Frequency == "weekly" && len(req.DaysOfWeek) == 0 {
			c.JSON(400, gin.H{"error": "days_of_week is required for weekly frequency"})
			return
		}
		if *req.Frequency == "monthly" {
			if req.DayOfMonth == nil {
				c.JSON(400, gin.H{"error": "day_of_month is required for monthly frequency"})
				return
			}
			if *req.DayOfMonth < 1 || *req.DayOfMonth > 31 {
				c.JSON(400, gin.H{"error": "day_of_month must be between 1 and 31"})
				return
			}
		}
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
		`SELECT user_id FROM recurring_expenses WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this recurring expense"})
		return
	}

	query := `UPDATE recurring_expenses SET updated_at = NOW()`
	args := []interface{}{}
	argCount := 1

	if req.Name != nil {
		query += fmt.Sprintf(", name = $%d", argCount)
		args = append(args, req.Name)
		argCount++
	}
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
	if req.Frequency != nil {
		query += fmt.Sprintf(", frequency = $%d", argCount)
		args = append(args, req.Frequency)
		argCount++
	}
	if req.DayOfMonth != nil {
		query += fmt.Sprintf(", day_of_month = $%d", argCount)
		args = append(args, req.DayOfMonth)
		argCount++
	}
	if req.DaysOfWeek != nil {
		query += fmt.Sprintf(", days_of_week = $%d", argCount)
		args = append(args, req.DaysOfWeek)
		argCount++
	}
	if req.StartDate != nil {
		query += fmt.Sprintf(", start_date = $%d", argCount)
		args = append(args, req.StartDate)
		argCount++
	}
	if req.EndDate != nil {
		query += fmt.Sprintf(", end_date = $%d", argCount)
		args = append(args, req.EndDate)
		argCount++
	}
	if req.IsActive != nil {
		query += fmt.Sprintf(", is_active = $%d", argCount)
		args = append(args, req.IsActive)
		argCount++
	}
	if req.Notes != nil {
		query += fmt.Sprintf(", notes = $%d", argCount)
		args = append(args, req.Notes)
		argCount++
	}

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at", argCount)
	args = append(args, id)

	var expense RecurringExpense
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&expense.ID, &expense.UserID, &expense.Name, &expense.Amount, &expense.Category,
		&expense.Frequency, &expense.DayOfMonth, &expense.DaysOfWeek, &expense.StartDate,
		&expense.EndDate, &expense.IsActive, &expense.LastAddedDate, &expense.Notes, &expense.CreatedAt, &expense.UpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update recurring expense"})
		return
	}

	c.JSON(200, expense)
}

func DeleteRecurringExpense(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring expense id"})
		return
	}

	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM recurring_expenses WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring expense not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this recurring expense"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM recurring_expenses WHERE id = $1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete recurring expense"})
		return
	}

	c.JSON(200, gin.H{"message": "recurring expense deleted successfully"})
}
