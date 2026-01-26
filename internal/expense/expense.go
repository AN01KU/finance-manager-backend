package expense

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
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
	TotalAmount decimal.Decimal             `json:"total_amount" validate:"required,gt=0"`
	Splits      []CreateExpenseSplitRequest `json:"splits" validate:"required,min=1,dive"`
}

type CreateExpenseSplitRequest struct {
	UserID uuid.UUID       `json:"user_id" validate:"required"`
	Amount decimal.Decimal `json:"amount" validate:"required,gte=0"`
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

	groupID := req.GroupID

	// Check if user is member of group
	var isMember bool
	err := db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, userID).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Validate splits: all users are members, sum == total
	splitSum := decimal.Zero
	userIDs := make(map[uuid.UUID]bool)
	for _, split := range req.Splits {
		if userIDs[split.UserID] {
			c.JSON(400, gin.H{"error": "duplicate user in splits"})
			return
		}
		userIDs[split.UserID] = true
		splitSum = splitSum.Add(split.Amount)
	}

	if !splitSum.Equal(req.TotalAmount) {
		c.JSON(400, gin.H{"error": "splits sum does not match total amount"})
		return
	}

	// Check all users are members
	for uid := range userIDs {
		err = db.Pool.QueryRow(c.Request.Context(),
			"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, uid).Scan(&isMember)
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
		groupID, req.Description, req.TotalAmount, userID).Scan(&exp.ID, &exp.GroupID, &exp.Description, &exp.TotalAmount, &exp.PaidBy, &exp.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create expense"})
		return
	}

	// Insert splits
	for _, split := range req.Splits {
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
	exp.Splits = make([]ExpenseSplit, len(req.Splits))
	for i, split := range req.Splits {
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
	var isMember bool
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, userID).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Get expenses
	rows, err := db.Pool.Query(c.Request.Context(),
		"SELECT id, group_id, description, total_amount, paid_by, created_at FROM expenses WHERE group_id = $1 ORDER BY created_at DESC",
		groupID)
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

	c.JSON(200, expenses)
}
