package group

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
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
	Email string `json:"email" validate:"required,email"`
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

	// Start transaction to create group and add creator as member
	tx, err := db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	var g Group
	err = tx.QueryRow(c.Request.Context(),
		"INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id, name, created_by, created_at",
		req.Name, userID).Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create group"})
		return
	}

	// Add creator as member
	_, err = tx.Exec(c.Request.Context(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)",
		g.ID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to add creator as member"})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit transaction"})
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

	// Check if user exists by email
	userID, exists, err := helpers.GetUserByEmail(c.Request.Context(), db, req.Email)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if !exists {
		c.JSON(404, gin.H{"error": "user not found with this email"})
		return
	}

	// Check if already member
	exists, err = helpers.IsGroupMember(c.Request.Context(), db, groupID, userID)
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
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)", groupID, userID)
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
	isMember, err := helpers.IsGroupMember(c.Request.Context(), db, groupID, userID)
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

func GetUserGroups(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT g.id, g.name, g.created_by, g.created_at 
		FROM groups g 
		JOIN group_members gm ON g.id = gm.group_id 
		WHERE gm.user_id = $1 
		ORDER BY g.created_at DESC`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get groups"})
		return
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group"})
			return
		}
		groups = append(groups, g)
	}

	c.JSON(200, groups)
}

type GroupMember struct {
	UserID   uuid.UUID `json:"user_id"`
	Email    string    `json:"email"`
	Username string    `json:"username"`
}

type GroupExpense struct {
	ID          uuid.UUID       `json:"id"`
	Description string          `json:"description"`
	TotalAmount decimal.Decimal `json:"total_amount"`
	PaidBy      uuid.UUID       `json:"paid_by"`
	CreatedAt   time.Time       `json:"created_at"`
}

type GroupDetails struct {
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	CreatedBy uuid.UUID      `json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	Members   []GroupMember  `json:"members"`
	Expenses  []GroupExpense `json:"expenses"`
}

func GetGroup(c *gin.Context, db *db.DB) {
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

	// Get group details
	var g Group
	err = db.Pool.QueryRow(c.Request.Context(),
		"SELECT id, name, created_by, created_at FROM groups WHERE id = $1",
		groupID).Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "group not found"})
		return
	}

	// Get members
	memberRows, err := db.Pool.Query(c.Request.Context(),
		`SELECT u.id, u.email, u.username 
		FROM users u 
		JOIN group_members gm ON u.id = gm.user_id 
		WHERE gm.group_id = $1`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer memberRows.Close()

	var members []GroupMember
	for memberRows.Next() {
		var m GroupMember
		if err := memberRows.Scan(&m.UserID, &m.Email, &m.Username); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		members = append(members, m)
	}

	// Get expenses
	expenseRows, err := db.Pool.Query(c.Request.Context(),
		`SELECT id, description, total_amount, paid_by, created_at 
		FROM expenses 
		WHERE group_id = $1 
		ORDER BY created_at DESC`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get expenses"})
		return
	}
	defer expenseRows.Close()

	var expenses []GroupExpense
	for expenseRows.Next() {
		var e GroupExpense
		if err := expenseRows.Scan(&e.ID, &e.Description, &e.TotalAmount, &e.PaidBy, &e.CreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan expense"})
			return
		}
		expenses = append(expenses, e)
	}

	// Check if current user is a member
	isMember := false
	for _, m := range members {
		if m.UserID == userID {
			isMember = true
			break
		}
	}

	c.JSON(200, gin.H{
		"group":     GroupDetails{g.ID, g.Name, g.CreatedBy, g.CreatedAt, members, expenses},
		"is_member": isMember,
	})
}
