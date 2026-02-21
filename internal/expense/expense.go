package expense

import (
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
	ID          uuid.UUID       `json:"id" db:"id"`
	GroupID     uuid.UUID       `json:"group_id" db:"group_id"`
	Description string          `json:"description" db:"description"`
	TotalAmount decimal.Decimal `json:"total_amount" db:"total_amount"`
	PaidBy      uuid.UUID       `json:"paid_by" db:"paid_by"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	Splits      []ExpenseSplit  `json:"splits,omitempty"`
}

type ExpenseSplit struct {
	ExpenseID uuid.UUID       `json:"expense_id" db:"expense_id"`
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	Amount    decimal.Decimal `json:"amount" db:"amount"`
}

type CreateExpenseRequest struct {
	GroupID     uuid.UUID                   `json:"group_id" validate:"required"`
	Description string                      `json:"description" validate:"required"`
	TotalAmount string                      `json:"total_amount" validate:"required,numeric"`
	Splits      []CreateExpenseSplitRequest `json:"splits" validate:"required,min=1,dive"`
}

type CreateExpenseSplitRequest struct {
	UserID uuid.UUID `json:"user_id" validate:"required"`
	Amount string    `json:"amount" validate:"required,numeric"`
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

	// Parse total amount
	totalAmount, err := decimal.NewFromString(req.TotalAmount)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid total amount format"})
		return
	}

	if totalAmount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "total amount must be greater than 0"})
		return
	}

	groupID := req.GroupID

	// Check if user is member of group
	isMember, err := helpers.IsGroupMember(c.Request.Context(), db, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Validate splits: all users are members, sum == total
	splitSum := decimal.Zero
	userIDs := make(map[uuid.UUID]bool)
	parsedSplits := make([]struct {
		UserID uuid.UUID
		Amount decimal.Decimal
	}, len(req.Splits))

	for i, split := range req.Splits {
		if userIDs[split.UserID] {
			c.JSON(400, gin.H{"error": "duplicate user in splits"})
			return
		}
		userIDs[split.UserID] = true

		// Parse split amount
		amount, err := decimal.NewFromString(split.Amount)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid split amount format"})
			return
		}

		if amount.LessThan(decimal.Zero) {
			c.JSON(400, gin.H{"error": "split amount cannot be negative"})
			return
		}

		parsedSplits[i].UserID = split.UserID
		parsedSplits[i].Amount = amount
		splitSum = splitSum.Add(amount)
	}

	if !splitSum.Equal(totalAmount) {
		c.JSON(400, gin.H{"error": "splits sum does not match total amount"})
		return
	}

	// Check all users are members
	for uid := range userIDs {
		isMember, err = helpers.IsGroupMember(c.Request.Context(), db, groupID, uid)
		if err != nil || !isMember {
			c.JSON(400, gin.H{"error": "all split users must be group members"})
			return
		}
	}

	// Start transaction
	tx, err := db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// Insert expense
	var exp Expense
	err = tx.QueryRow(c.Request.Context(),
		"INSERT INTO expenses (group_id, description, total_amount, paid_by) VALUES ($1, $2, $3, $4) RETURNING id, group_id, description, total_amount, paid_by, created_at",
		groupID, req.Description, totalAmount, userID).Scan(&exp.ID, &exp.GroupID, &exp.Description, &exp.TotalAmount, &exp.PaidBy, &exp.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create expense"})
		return
	}

	// Insert splits
	for _, split := range parsedSplits {
		_, err = tx.Exec(c.Request.Context(),
			"INSERT INTO expense_splits (expense_id, user_id, amount) VALUES ($1, $2, $3)",
			exp.ID, split.UserID, split.Amount)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create expense split"})
			return
		}
	}

	// Commit
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit transaction"})
		return
	}

	// Load splits for response
	exp.Splits = make([]ExpenseSplit, len(parsedSplits))
	for i, split := range parsedSplits {
		exp.Splits[i] = ExpenseSplit{
			ExpenseID: exp.ID,
			UserID:    split.UserID,
			Amount:    split.Amount,
		}
	}

	c.JSON(201, exp)
}

func GetGroupExpenses(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupIDStr := c.Param("id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	// Check if user is member of group
	isMember, err := helpers.IsGroupMember(c.Request.Context(), db, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Parse pagination parameters
	limit := 50 // default
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

	// Get expenses with pagination
	rows, err := db.Pool.Query(c.Request.Context(),
		"SELECT id, group_id, description, total_amount, paid_by, created_at FROM expenses WHERE group_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3",
		groupID, limit, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get expenses"})
		return
	}
	defer rows.Close()

	var expenses []Expense
	for rows.Next() {
		var exp Expense
		if err := rows.Scan(&exp.ID, &exp.GroupID, &exp.Description, &exp.TotalAmount, &exp.PaidBy, &exp.CreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		expenses = append(expenses, exp)
	}

	// Get total count for pagination metadata
	var totalCount int
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM expenses WHERE group_id = $1", groupID).Scan(&totalCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get total count"})
		return
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

func GetUserExpenses(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT DISTINCT e.id, e.group_id, e.description, e.total_amount, e.paid_by, e.created_at
		FROM expenses e
		LEFT JOIN expense_splits es ON e.id = es.expense_id
		WHERE e.paid_by = $1 OR es.user_id = $1
		ORDER BY e.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get expenses"})
		return
	}
	defer rows.Close()

	var expenses []Expense
	for rows.Next() {
		var exp Expense
		if err := rows.Scan(&exp.ID, &exp.GroupID, &exp.Description, &exp.TotalAmount, &exp.PaidBy, &exp.CreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		expenses = append(expenses, exp)
	}

	var totalCount int
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT e.id) FROM expenses e
		LEFT JOIN expense_splits es ON e.id = es.expense_id
		WHERE e.paid_by = $1 OR es.user_id = $1`, userID).Scan(&totalCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get total count"})
		return
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
