package recurring

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

var validate = validator.New()

type RecurringTransaction struct {
	ID             uuid.UUID            `json:"id"`
	UserID         uuid.UUID            `json:"user_id"`
	Type           string               `json:"type"`
	Name           string               `json:"name"`
	Amount         float64              `json:"amount"`
	Category       string               `json:"category"`
	Frequency      string               `json:"frequency"`
	DayOfMonth     *int                 `json:"day_of_month,omitempty"`
	DaysOfWeek     []int                `json:"days_of_week,omitempty"`
	StartDate      helpers.EpochMillis  `json:"start_date"`
	EndDate        *helpers.EpochMillis `json:"end_date,omitempty"`
	IsActive       bool                 `json:"is_active"`
	LastAddedDate  *helpers.EpochMillis `json:"last_added_date,omitempty"`
	Notes          *string              `json:"notes,omitempty"`
	NextOccurrence *helpers.EpochMillis `json:"next_occurrence"`
	CreatedAt      helpers.EpochMillis  `json:"created_at"`
	UpdatedAt      helpers.EpochMillis  `json:"updated_at"`
}

type CreateRecurringTransactionRequest struct {
	ID         *uuid.UUID `json:"id,omitempty"`
	Type       string     `json:"type" validate:"required,oneof=expense income"`
	Name       string     `json:"name" validate:"required"`
	Amount     float64    `json:"amount" validate:"required"`
	Category   string     `json:"category" validate:"required"`
	Frequency  string     `json:"frequency" validate:"required,oneof=daily weekly monthly yearly"`
	DayOfMonth *int       `json:"day_of_month,omitempty" validate:"omitempty,min=1,max=31"`
	DaysOfWeek []int      `json:"days_of_week,omitempty" validate:"omitempty,dive,min=0,max=6"`
	StartDate  int64      `json:"start_date" validate:"required"`
	EndDate    *int64     `json:"end_date,omitempty"`
	Notes      *string    `json:"notes,omitempty"`
}

type UpdateRecurringTransactionRequest struct {
	Type       *string  `json:"type,omitempty" validate:"omitempty,oneof=expense income"`
	Name       *string  `json:"name,omitempty" validate:"omitempty"`
	Amount     *float64 `json:"amount,omitempty"`
	Category   *string  `json:"category,omitempty" validate:"omitempty"`
	Frequency  *string  `json:"frequency,omitempty" validate:"omitempty,oneof=daily weekly monthly yearly"`
	DayOfMonth *int     `json:"day_of_month,omitempty" validate:"omitempty,min=1,max=31"`
	DaysOfWeek []int    `json:"days_of_week,omitempty" validate:"omitempty,dive,min=0,max=6"`
	StartDate  *int64   `json:"start_date,omitempty"`
	EndDate    *int64   `json:"end_date,omitempty"`
	IsActive   *bool    `json:"is_active,omitempty"`
	Notes      *string  `json:"notes,omitempty"`
}

func CreateRecurringTransaction(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateRecurringTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

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

	if req.EndDate != nil && *req.EndDate <= req.StartDate {
		c.JSON(400, gin.H{"error": "end_date must be after start_date"})
		return
	}

	startDate := time.UnixMilli(req.StartDate).UTC()
	var endDate *time.Time
	if req.EndDate != nil {
		t := time.UnixMilli(*req.EndDate).UTC()
		endDate = &t
	}

	amount := decimal.NewFromFloat(req.Amount)

	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be greater than 0"})
		return
	}

	recurringID := uuid.New()
	if req.ID != nil {
		recurringID = *req.ID
	}

	var rt RecurringTransaction
	var rawStartDate, rawCreatedAt, rawUpdatedAt time.Time
	var rawEndDate, rawLastAddedDate *time.Time
	err := db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO recurring_transactions (id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, notes, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, true, $12, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type,
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
		 RETURNING id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at`,
		recurringID, userID, req.Type, req.Name, amount, req.Category, req.Frequency, req.DayOfMonth, req.DaysOfWeek, startDate, endDate, req.Notes).Scan(
		&rt.ID, &rt.UserID, &rt.Type, &rt.Name, &rt.Amount, &rt.Category,
		&rt.Frequency, &rt.DayOfMonth, &rt.DaysOfWeek, &rawStartDate,
		&rawEndDate, &rt.IsActive, &rawLastAddedDate, &rt.Notes, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create recurring transaction"})
		return
	}
	rt.StartDate = helpers.FromTime(rawStartDate)
	rt.EndDate = helpers.FromTimePtr(rawEndDate)
	rt.LastAddedDate = helpers.FromTimePtr(rawLastAddedDate)
	rt.CreatedAt = helpers.FromTime(rawCreatedAt)
	rt.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	rt.NextOccurrence = computeNextOccurrence(&rt)

	c.JSON(201, rt)
}

func ListRecurringTransactions(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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

	query := `SELECT id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at, COUNT(*) OVER() AS total_count
		      FROM recurring_transactions
		      WHERE user_id = $1`
	args := []interface{}{userID}

	if activeStr := c.Query("active"); activeStr == "true" {
		query += " AND is_active = true"
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := db.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve recurring transactions"})
		return
	}
	defer rows.Close()

	var transactions []RecurringTransaction
	var total int
	for rows.Next() {
		var rt RecurringTransaction
		var rawStartDate, rawCreatedAt, rawUpdatedAt time.Time
		var rawEndDate, rawLastAddedDate *time.Time
		if err := rows.Scan(&rt.ID, &rt.UserID, &rt.Type, &rt.Name, &rt.Amount, &rt.Category,
			&rt.Frequency, &rt.DayOfMonth, &rt.DaysOfWeek, &rawStartDate,
			&rawEndDate, &rt.IsActive, &rawLastAddedDate, &rt.Notes, &rawCreatedAt, &rawUpdatedAt,
			&total); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan recurring transaction"})
			return
		}
		rt.StartDate = helpers.FromTime(rawStartDate)
		rt.EndDate = helpers.FromTimePtr(rawEndDate)
		rt.LastAddedDate = helpers.FromTimePtr(rawLastAddedDate)
		rt.CreatedAt = helpers.FromTime(rawCreatedAt)
		rt.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		rt.NextOccurrence = computeNextOccurrence(&rt)
		transactions = append(transactions, rt)
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}

	if transactions == nil {
		transactions = []RecurringTransaction{}
	}

	c.JSON(200, gin.H{
		"data": transactions,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  total,
		},
	})
}

func GetRecurringTransaction(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring transaction id"})
		return
	}

	var rt RecurringTransaction
	var rawStartDate, rawCreatedAt, rawUpdatedAt time.Time
	var rawEndDate, rawLastAddedDate *time.Time
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at
		 FROM recurring_transactions
		 WHERE id = $1 AND user_id = $2`,
		id, userID).Scan(&rt.ID, &rt.UserID, &rt.Type, &rt.Name, &rt.Amount, &rt.Category,
		&rt.Frequency, &rt.DayOfMonth, &rt.DaysOfWeek, &rawStartDate,
		&rawEndDate, &rt.IsActive, &rawLastAddedDate, &rt.Notes, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring transaction not found"})
		return
	}
	rt.StartDate = helpers.FromTime(rawStartDate)
	rt.EndDate = helpers.FromTimePtr(rawEndDate)
	rt.LastAddedDate = helpers.FromTimePtr(rawLastAddedDate)
	rt.CreatedAt = helpers.FromTime(rawCreatedAt)
	rt.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	rt.NextOccurrence = computeNextOccurrence(&rt)

	c.JSON(200, rt)
}

func UpdateRecurringTransaction(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring transaction id"})
		return
	}

	var req UpdateRecurringTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

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
		a := decimal.NewFromFloat(*req.Amount)
		if a.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be greater than 0"})
			return
		}
		parsedAmount = &a
	}

	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM recurring_transactions WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring transaction not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this recurring transaction"})
		return
	}

	// If any schedule-affecting field changes, reset last_added_date so the
	// generator re-evaluates from start_date under the new rule. Combined with
	// the (user_id, recurring_transaction_id, date) unique index and the
	// recurring_skipped_occurrences table, this is safe — we won't duplicate
	// already-generated instances and we won't regenerate user-deleted ones.
	scheduleChanged := req.Frequency != nil || req.DayOfMonth != nil || req.DaysOfWeek != nil || req.StartDate != nil

	query := `UPDATE recurring_transactions SET updated_at = NOW()`
	args := []interface{}{}
	argCount := 1

	if scheduleChanged {
		query += `, last_added_date = NULL`
	}

	if req.Type != nil {
		query += fmt.Sprintf(", type = $%d", argCount)
		args = append(args, req.Type)
		argCount++
	}
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
		args = append(args, time.UnixMilli(*req.StartDate).UTC())
		argCount++
	}
	if req.EndDate != nil {
		query += fmt.Sprintf(", end_date = $%d", argCount)
		args = append(args, time.UnixMilli(*req.EndDate).UTC())
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

	query += fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes, created_at, updated_at", argCount)
	args = append(args, id)

	var rt RecurringTransaction
	var rawStartDate, rawCreatedAt, rawUpdatedAt time.Time
	var rawEndDate, rawLastAddedDate *time.Time
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&rt.ID, &rt.UserID, &rt.Type, &rt.Name, &rt.Amount, &rt.Category,
		&rt.Frequency, &rt.DayOfMonth, &rt.DaysOfWeek, &rawStartDate,
		&rawEndDate, &rt.IsActive, &rawLastAddedDate, &rt.Notes, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update recurring transaction"})
		return
	}
	rt.StartDate = helpers.FromTime(rawStartDate)
	rt.EndDate = helpers.FromTimePtr(rawEndDate)
	rt.LastAddedDate = helpers.FromTimePtr(rawLastAddedDate)
	rt.CreatedAt = helpers.FromTime(rawCreatedAt)
	rt.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	rt.NextOccurrence = computeNextOccurrence(&rt)

	c.JSON(200, rt)
}

func DeleteRecurringTransaction(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid recurring transaction id"})
		return
	}

	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM recurring_transactions WHERE id = $1`, id).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "recurring transaction not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this recurring transaction"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM recurring_transactions WHERE id = $1`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete recurring transaction"})
		return
	}

	c.JSON(200, gin.H{"message": "recurring transaction deleted successfully"})
}

// computeNextOccurrence populates the NextOccurrence field on a RecurringTransaction
// after its date fields have been assigned from a DB scan.
func computeNextOccurrence(rt *RecurringTransaction) *helpers.EpochMillis {
	if !rt.IsActive {
		return nil
	}
	var endDate *time.Time
	if rt.EndDate != nil {
		t := rt.EndDate.Time
		endDate = &t
	}
	// Use lastAddedDate as the baseline if set, otherwise startDate
	baseline := rt.StartDate.Time
	if rt.LastAddedDate != nil {
		baseline = rt.LastAddedDate.Time
	}
	next := NextFutureOccurrence(baseline, rt.Frequency, rt.DayOfMonth, rt.DaysOfWeek, startOfDay(time.Now()))
	if next == nil {
		return nil
	}
	if endDate != nil && next.After(*endDate) {
		return nil
	}
	e := helpers.FromTime(*next)
	return &e
}
