package group

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type Group struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	CreatedBy uuid.UUID `json:"created_by" db:"created_by"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type CreateGroupRequest struct {
	Name string `json:"name" validate:"required,min=1"`
}

type AddMemberRequest struct {
	UserID uuid.UUID `json:"user_id" validate:"required"`
}

type Balance struct {
	UserID uuid.UUID       `json:"user_id"`
	Amount decimal.Decimal `json:"amount"`
}

func CreateGroup(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var g Group
	err := db.Pool.QueryRow(c.Request.Context(),
		"INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id, name, created_by, created_at",
		req.Name, userID).Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create group"})
		return
	}

	c.JSON(201, g)
}

func AddMember(c *gin.Context, db *db.DB) {
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

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Check if user exists
	var exists bool
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)", req.UserID).Scan(&exists)
	if err != nil || !exists {
		c.JSON(400, gin.H{"error": "user does not exist"})
		return
	}

	// Check if already member
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)", groupID, req.UserID).Scan(&exists)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if exists {
		c.JSON(400, gin.H{"error": "user already in group"})
		return
	}

	// Add member
	_, err = db.Pool.Exec(c.Request.Context(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)", groupID, req.UserID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to add member"})
		return
	}

	c.JSON(200, gin.H{"message": "member added"})
}

func GetBalances(c *gin.Context, db *db.DB) {
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

	// Get all members
	rows, err := db.Pool.Query(c.Request.Context(),
		"SELECT user_id FROM group_members WHERE group_id = $1", groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer rows.Close()

	members := make(map[uuid.UUID]decimal.Decimal)
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		members[uid] = decimal.Zero
	}

	// Add from expenses: paid_by gets +total, split users get -amount
	expRows, err := db.Pool.Query(c.Request.Context(),
		"SELECT paid_by, total_amount FROM expenses WHERE group_id = $1", groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get expenses"})
		return
	}
	defer expRows.Close()

	for expRows.Next() {
		var paidBy uuid.UUID
		var total decimal.Decimal
		if err := expRows.Scan(&paidBy, &total); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		if bal, ok := members[paidBy]; ok {
			members[paidBy] = bal.Add(total)
		}
	}

	splitRows, err := db.Pool.Query(c.Request.Context(),
		"SELECT es.user_id, es.amount FROM expense_splits es JOIN expenses e ON es.expense_id = e.id WHERE e.group_id = $1", groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get expense splits"})
		return
	}
	defer splitRows.Close()

	for splitRows.Next() {
		var uid uuid.UUID
		var amt decimal.Decimal
		if err := splitRows.Scan(&uid, &amt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		if bal, ok := members[uid]; ok {
			members[uid] = bal.Sub(amt)
		}
	}

	// Subtract settlements: from_user -amount, to_user +amount
	settRows, err := db.Pool.Query(c.Request.Context(),
		"SELECT from_user, to_user, amount FROM settlements WHERE group_id = $1", groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlements"})
		return
	}
	defer settRows.Close()

	for settRows.Next() {
		var from, to uuid.UUID
		var amt decimal.Decimal
		if err := settRows.Scan(&from, &to, &amt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan settlement"})
			return
		}
		if bal, ok := members[from]; ok {
			members[from] = bal.Sub(amt)
		}
		if bal, ok := members[to]; ok {
			members[to] = bal.Add(amt)
		}
	}

	// Convert to slice
	var balances []Balance
	for uid, amt := range members {
		balances = append(balances, Balance{UserID: uid, Amount: amt})
	}

	c.JSON(200, balances)
}
