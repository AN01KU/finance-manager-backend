package group

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

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Group struct {
	ID        uuid.UUID           `json:"id"`
	Name      string              `json:"name"`
	CreatedBy uuid.UUID           `json:"created_by"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
}

type GroupMember struct {
	UserID    uuid.UUID           `json:"id"`
	Email     string              `json:"email"`
	Username  string              `json:"username"`
	CreatedAt helpers.EpochMillis `json:"joined_at"`
}

type Balance struct {
	UserID uuid.UUID `json:"user_id"`
	Amount float64   `json:"amount"`
}

type GroupWithDetails struct {
	ID        uuid.UUID           `json:"id"`
	Name      string              `json:"name"`
	CreatedBy uuid.UUID           `json:"created_by"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
	Members   []GroupMember       `json:"members"`
	Balances  []Balance           `json:"balances"`
}

type GroupTransaction struct {
	ID           uuid.UUID             `json:"id"`
	GroupID      uuid.UUID             `json:"group_id"`
	PaidByUserID uuid.UUID             `json:"paid_by_user_id"`
	TotalAmount  float64               `json:"total_amount"`
	Category     string                `json:"category"`
	Date         helpers.EpochMillis   `json:"date"`
	Description  *string               `json:"description,omitempty"`
	Notes        *string               `json:"notes,omitempty"`
	IsDeleted    bool                  `json:"is_deleted"`
	CreatedAt    helpers.EpochMillis   `json:"created_at"`
	UpdatedAt    helpers.EpochMillis   `json:"updated_at"`
	Splits       []SplitDetail         `json:"splits"`
}

type SplitDetail struct {
	ID            uuid.UUID             `json:"id"`
	UserID        uuid.UUID             `json:"user_id"`
	Amount        float64    `json:"amount"`
	TransactionID *uuid.UUID `json:"transaction_id,omitempty"`
}

type SplitInput struct {
	UserID uuid.UUID `json:"user_id" validate:"required"`
	Amount float64   `json:"amount" validate:"required"`
}

type CreateGroupTransactionRequest struct {
	ID           *uuid.UUID   `json:"id,omitempty"`
	PaidByUserID uuid.UUID    `json:"paid_by_user_id" validate:"required"`
	TotalAmount  float64      `json:"total_amount" validate:"required"`
	Category     string       `json:"category" validate:"required,max=100"`
	Date         int64        `json:"date" validate:"required"`
	Description  *string      `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes        *string      `json:"notes,omitempty"`
	Splits       []SplitInput `json:"splits" validate:"required,min=1,dive"`
}

type UpdateGroupTransactionRequest struct {
	Category    *string `json:"category,omitempty" validate:"omitempty,max=100"`
	Date        *int64  `json:"date,omitempty"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=255"`
	Notes       *string `json:"notes,omitempty"`
}

type CreateGroupRequest struct {
	Name string `json:"name" validate:"required,min=1"`
}

type AddMemberRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ---------------------------------------------------------------------------
// Group CRUD
// ---------------------------------------------------------------------------

func CreateGroup(c *gin.Context, database *db.DB) {
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

	tx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	var g Group
	var rawGroupCreatedAt time.Time
	err = tx.QueryRow(c.Request.Context(),
		"INSERT INTO groups (name, created_by) VALUES ($1, $2) RETURNING id, name, created_by, created_at",
		req.Name, userID).Scan(&g.ID, &g.Name, &g.CreatedBy, &rawGroupCreatedAt)
	g.CreatedAt = helpers.FromTime(rawGroupCreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create group"})
		return
	}

	_, err = tx.Exec(c.Request.Context(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)", g.ID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to add creator as member"})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(201, g)
}

func GetUserGroups(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	rows, err := database.Pool.Query(c.Request.Context(),
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
	var groupIDs []uuid.UUID
	for rows.Next() {
		var g Group
		var rawCreatedAt time.Time
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &rawCreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group"})
			return
		}
		g.CreatedAt = helpers.FromTime(rawCreatedAt)
		groups = append(groups, g)
		groupIDs = append(groupIDs, g.ID)
	}

	if len(groupIDs) == 0 {
		c.JSON(200, gin.H{"data": []GroupWithDetails{}})
		return
	}

	// Fetch members for all groups
	memberRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT gm.group_id, u.id, u.email, u.username, gm.joined_at
		 FROM group_members gm
		 JOIN users u ON gm.user_id = u.id
		 WHERE gm.group_id = ANY($1)
		 ORDER BY gm.group_id, gm.joined_at`, groupIDs)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer memberRows.Close()

	membersByGroup := make(map[uuid.UUID][]GroupMember)
	for memberRows.Next() {
		var gid uuid.UUID
		var m GroupMember
		var rawJoinedAt time.Time
		if err := memberRows.Scan(&gid, &m.UserID, &m.Email, &m.Username, &rawJoinedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		m.CreatedAt = helpers.FromTime(rawJoinedAt)
		membersByGroup[gid] = append(membersByGroup[gid], m)
	}

	// Fetch balance data: payer side from group_transactions
	gtRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT group_id, paid_by_user_id, total_amount
		 FROM group_transactions
		 WHERE group_id = ANY($1) AND is_deleted = FALSE`, groupIDs)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get group transactions"})
		return
	}
	defer gtRows.Close()

	payersByGroup := make(map[uuid.UUID][]payerEntry)
	for gtRows.Next() {
		var gid, paidBy uuid.UUID
		var amount decimal.Decimal
		if err := gtRows.Scan(&gid, &paidBy, &amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group transaction"})
			return
		}
		payersByGroup[gid] = append(payersByGroup[gid], payerEntry{paidBy, amount})
	}

	// Fetch split side
	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT gts.user_id, gts.amount, gt.group_id
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gt.group_id = ANY($1) AND gt.is_deleted = FALSE`, groupIDs)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	splitsByGroup := make(map[uuid.UUID][]splitEntry)
	for splitRows.Next() {
		var uid, gid uuid.UUID
		var amount decimal.Decimal
		if err := splitRows.Scan(&uid, &amount, &gid); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		splitsByGroup[gid] = append(splitsByGroup[gid], splitEntry{uid, amount})
	}

	// Fetch settlements
	settRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT from_user, to_user, amount, group_id
		 FROM settlements WHERE group_id = ANY($1)`, groupIDs)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlements"})
		return
	}
	defer settRows.Close()

	settByGroup := make(map[uuid.UUID][]settlementEntry)
	for settRows.Next() {
		var from, to, gid uuid.UUID
		var amount decimal.Decimal
		if err := settRows.Scan(&from, &to, &amount, &gid); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan settlement"})
			return
		}
		settByGroup[gid] = append(settByGroup[gid], settlementEntry{from, to, amount})
	}

	var result []GroupWithDetails
	for _, g := range groups {
		members := membersByGroup[g.ID]
		if members == nil {
			members = []GroupMember{}
		}

		balances := computeBalances(members, payersByGroup[g.ID], splitsByGroup[g.ID], settByGroup[g.ID])

		result = append(result, GroupWithDetails{
			ID:        g.ID,
			Name:      g.Name,
			CreatedBy: g.CreatedBy,
			CreatedAt: g.CreatedAt,
			Members:   members,
			Balances:  balances,
		})
	}

	c.JSON(200, gin.H{"data": result})
}

func GetGroup(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	var g Group
	var rawGroupCreatedAt2 time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		"SELECT id, name, created_by, created_at FROM groups WHERE id = $1", groupID,
	).Scan(&g.ID, &g.Name, &g.CreatedBy, &rawGroupCreatedAt2)
	g.CreatedAt = helpers.FromTime(rawGroupCreatedAt2)
	if err != nil {
		c.JSON(404, gin.H{"error": "group not found"})
		return
	}

	memberRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT u.id, u.email, u.username, gm.joined_at
		 FROM users u
		 JOIN group_members gm ON u.id = gm.user_id
		 WHERE gm.group_id = $1 ORDER BY gm.joined_at`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer memberRows.Close()

	var members []GroupMember
	isMember := false
	for memberRows.Next() {
		var m GroupMember
		var rawJoinedAt time.Time
		if err := memberRows.Scan(&m.UserID, &m.Email, &m.Username, &rawJoinedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		m.CreatedAt = helpers.FromTime(rawJoinedAt)
		if m.UserID == userID {
			isMember = true
		}
		members = append(members, m)
	}
	if members == nil {
		members = []GroupMember{}
	}

	gtRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT paid_by_user_id, total_amount FROM group_transactions
		 WHERE group_id = $1 AND is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get group transactions"})
		return
	}
	defer gtRows.Close()

	var payers []payerEntry
	for gtRows.Next() {
		var e payerEntry
		if err := gtRows.Scan(&e.paidBy, &e.amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group transaction"})
			return
		}
		payers = append(payers, e)
	}

	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT gts.user_id, gts.amount
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gt.group_id = $1 AND gt.is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	var splits []splitEntry
	for splitRows.Next() {
		var s splitEntry
		if err := splitRows.Scan(&s.userID, &s.amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		splits = append(splits, s)
	}

	settRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, from_user, to_user, amount, notes, created_at FROM settlements WHERE group_id = $1 ORDER BY created_at DESC`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlements"})
		return
	}
	defer settRows.Close()

	type settlementResponse struct {
		ID        uuid.UUID           `json:"id"`
		FromUser  uuid.UUID           `json:"from_user"`
		ToUser    uuid.UUID           `json:"to_user"`
		Amount    float64             `json:"amount"`
		Notes     *string             `json:"notes,omitempty"`
		CreatedAt helpers.EpochMillis `json:"created_at"`
	}
	var setts []settlementEntry
	settlementsResp := []settlementResponse{}
	for settRows.Next() {
		var s settlementEntry
		var sr settlementResponse
		var rawCreatedAt time.Time
		if err := settRows.Scan(&sr.ID, &s.from, &s.to, &s.amount, &sr.Notes, &rawCreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan settlement"})
			return
		}
		sr.FromUser = s.from
		sr.ToUser = s.to
		sr.Amount = s.amount.InexactFloat64()
		sr.CreatedAt = helpers.FromTime(rawCreatedAt)
		setts = append(setts, s)
		settlementsResp = append(settlementsResp, sr)
	}

	balances := computeBalances(members, payers, splits, setts)

	c.JSON(200, gin.H{
		"group": gin.H{
			"id":          g.ID,
			"name":        g.Name,
			"created_by":  g.CreatedBy,
			"created_at":  g.CreatedAt,
			"members":     members,
			"balances":    balances,
			"settlements": settlementsResp,
		},
		"is_member": isMember,
	})
}

func AddMember(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
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

	newUserID, exists, err := helpers.GetUserByEmail(c.Request.Context(), database, req.Email)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if !exists {
		c.JSON(404, gin.H{"error": "user not found with this email"})
		return
	}

	already, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, newUserID)
	if err != nil {
		c.JSON(500, gin.H{"error": "database error"})
		return
	}
	if already {
		c.JSON(400, gin.H{"error": "user already in group"})
		return
	}

	_, err = database.Pool.Exec(c.Request.Context(),
		"INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)", groupID, newUserID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to add member"})
		return
	}

	c.JSON(200, gin.H{"message": "member added"})
}

func GetMembers(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	rows, err := database.Pool.Query(c.Request.Context(),
		`SELECT u.id, u.email, u.username, gm.joined_at
		 FROM users u
		 JOIN group_members gm ON u.id = gm.user_id
		 WHERE gm.group_id = $1 ORDER BY gm.joined_at ASC`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer rows.Close()

	members := []GroupMember{}
	for rows.Next() {
		var m GroupMember
		var rawJoinedAt time.Time
		if err := rows.Scan(&m.UserID, &m.Email, &m.Username, &rawJoinedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		m.CreatedAt = helpers.FromTime(rawJoinedAt)
		members = append(members, m)
	}

	c.JSON(200, gin.H{"data": members})
}

func GetBalances(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	memberRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT u.id, u.email, u.username, gm.joined_at
		 FROM users u JOIN group_members gm ON u.id = gm.user_id
		 WHERE gm.group_id = $1`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get members"})
		return
	}
	defer memberRows.Close()

	var members []GroupMember
	for memberRows.Next() {
		var m GroupMember
		var rawJoinedAt time.Time
		if err := memberRows.Scan(&m.UserID, &m.Email, &m.Username, &rawJoinedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan member"})
			return
		}
		m.CreatedAt = helpers.FromTime(rawJoinedAt)
		members = append(members, m)
	}

	gtRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT paid_by_user_id, total_amount FROM group_transactions
		 WHERE group_id = $1 AND is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get group transactions"})
		return
	}
	defer gtRows.Close()

	var payers []payerEntry
	for gtRows.Next() {
		var e payerEntry
		if err := gtRows.Scan(&e.paidBy, &e.amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group transaction"})
			return
		}
		payers = append(payers, e)
	}

	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT gts.user_id, gts.amount
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		 WHERE gt.group_id = $1 AND gt.is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	var splits []splitEntry
	for splitRows.Next() {
		var s splitEntry
		if err := splitRows.Scan(&s.userID, &s.amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		splits = append(splits, s)
	}

	settRows, err := database.Pool.Query(c.Request.Context(),
		"SELECT from_user, to_user, amount FROM settlements WHERE group_id = $1", groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlements"})
		return
	}
	defer settRows.Close()

	var setts []settlementEntry
	for settRows.Next() {
		var s settlementEntry
		if err := settRows.Scan(&s.from, &s.to, &s.amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan settlement"})
			return
		}
		setts = append(setts, s)
	}

	balances := computeBalances(members, payers, splits, setts)
	c.JSON(200, gin.H{"data": balances})
}

func GetGroupSettlements(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	limit := 20
	if s := c.Query("limit"); s != "" {
		if l, err := strconv.Atoi(s); err == nil && l > 0 {
			limit = l
		}
	}
	offset := 0
	if s := c.Query("offset"); s != "" {
		if o, err := strconv.Atoi(s); err == nil && o >= 0 {
			offset = o
		}
	}

	var total int
	if err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM settlements WHERE group_id = $1`, groupID).Scan(&total); err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlement count"})
		return
	}

	rows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, from_user, to_user, amount, notes, created_at
		 FROM settlements WHERE group_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, groupID, limit, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get settlements"})
		return
	}
	defer rows.Close()

	type settlementItem struct {
		ID        uuid.UUID           `json:"id"`
		FromUser  uuid.UUID           `json:"from_user"`
		ToUser    uuid.UUID           `json:"to_user"`
		Amount    float64             `json:"amount"`
		Notes     *string             `json:"notes,omitempty"`
		CreatedAt helpers.EpochMillis `json:"created_at"`
	}
	result := []settlementItem{}
	for rows.Next() {
		var item settlementItem
		var rawCreatedAt time.Time
		if err := rows.Scan(&item.ID, &item.FromUser, &item.ToUser, &item.Amount, &item.Notes, &rawCreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan settlement"})
			return
		}
		item.CreatedAt = helpers.FromTime(rawCreatedAt)
		result = append(result, item)
	}

	c.JSON(200, gin.H{
		"data": result,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
			"total":  total,
		},
	})
}

// ---------------------------------------------------------------------------
// Group Transactions
// ---------------------------------------------------------------------------

func CreateGroupTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	var req CreateGroupTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	totalAmount := decimal.NewFromFloat(req.TotalAmount)
	if totalAmount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "invalid total_amount"})
		return
	}

	// Parse and validate split amounts
	splitAmounts := make([]decimal.Decimal, len(req.Splits))
	splitSum := decimal.Zero
	for i, s := range req.Splits {
		amt := decimal.NewFromFloat(s.Amount)
		if amt.LessThan(decimal.Zero) {
			c.JSON(400, gin.H{"error": fmt.Sprintf("invalid amount for split %d", i)})
			return
		}
		splitAmounts[i] = amt
		splitSum = splitSum.Add(amt)
	}

	if !splitSum.Equal(totalAmount) {
		c.JSON(400, gin.H{"error": fmt.Sprintf("splits sum (%s) must equal total_amount (%s)", splitSum, totalAmount)})
		return
	}

	// Validate paid_by is a member
	paidByIsMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, req.PaidByUserID)
	if err != nil || !paidByIsMember {
		c.JSON(400, gin.H{"error": "paid_by_user_id is not a member of the group"})
		return
	}

	// Validate all split users are members
	for _, s := range req.Splits {
		ok, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, s.UserID)
		if err != nil || !ok {
			c.JSON(400, gin.H{"error": fmt.Sprintf("user %s is not a member of the group", s.UserID)})
			return
		}
	}

	gtDate := time.UnixMilli(req.Date).UTC()

	gtID := uuid.New()
	if req.ID != nil {
		gtID = *req.ID
	}

	tx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context())

	// Insert group_transaction
	var gt GroupTransaction
	var rawGTDate, rawGTCreatedAt, rawGTUpdatedAt time.Time
	err = tx.QueryRow(c.Request.Context(),
		`INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, description, notes, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (id) DO UPDATE SET
		   paid_by_user_id = EXCLUDED.paid_by_user_id,
		   total_amount = EXCLUDED.total_amount,
		   category = EXCLUDED.category,
		   date = EXCLUDED.date,
		   description = EXCLUDED.description,
		   notes = EXCLUDED.notes,
		   updated_at = NOW()
		 RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`,
		gtID, groupID, req.PaidByUserID, totalAmount, req.Category, gtDate, req.Description, req.Notes,
	).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawGTDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawGTCreatedAt, &rawGTUpdatedAt,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create group transaction"})
		return
	}
	gt.Date = helpers.FromTime(rawGTDate)
	gt.CreatedAt = helpers.FromTime(rawGTCreatedAt)
	gt.UpdatedAt = helpers.FromTime(rawGTUpdatedAt)

	// For each split: create a personal expense transaction and insert the split row.
	// Payer gets full total_amount (reflects what they paid); non-payers get their split amount.
	var splits []SplitDetail
	for i, s := range req.Splits {
		txAmount := splitAmounts[i]
		if s.UserID == req.PaidByUserID {
			txAmount = totalAmount
		}

		var memberTxID uuid.UUID
		err = tx.QueryRow(c.Request.Context(),
			`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_transaction_id, updated_at)
			 VALUES ($1, 'expense', $2, $3, $4, $5, $6, $7, NOW())
			 RETURNING id`,
			s.UserID, txAmount, req.Category, gtDate, req.Description, req.Notes, gt.ID,
		).Scan(&memberTxID)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create personal transaction for member"})
			return
		}

		var split SplitDetail
		err = tx.QueryRow(c.Request.Context(),
			`INSERT INTO group_transaction_splits (group_transaction_id, user_id, amount, transaction_id)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id, user_id, amount, transaction_id`,
			gt.ID, s.UserID, splitAmounts[i], memberTxID,
		).Scan(&split.ID, &split.UserID, &split.Amount, &split.TransactionID)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create split"})
			return
		}
		splits = append(splits, split)
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	gt.Splits = splits
	c.JSON(201, gt)
}

func ListGroupTransactions(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	rows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at
		 FROM group_transactions
		 WHERE group_id = $1 AND is_deleted = FALSE
		 ORDER BY date DESC, created_at DESC`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve group transactions"})
		return
	}
	defer rows.Close()

	var gts []GroupTransaction
	var gtIDs []uuid.UUID
	for rows.Next() {
		var gt GroupTransaction
		var rawDate, rawCreatedAt, rawUpdatedAt time.Time
		if err := rows.Scan(
			&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawDate,
			&gt.Description, &gt.Notes, &gt.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
		); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan group transaction"})
			return
		}
		gt.Date = helpers.FromTime(rawDate)
		gt.CreatedAt = helpers.FromTime(rawCreatedAt)
		gt.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		gt.Splits = []SplitDetail{}
		gts = append(gts, gt)
		gtIDs = append(gtIDs, gt.ID)
	}

	if len(gtIDs) == 0 {
		c.JSON(200, gin.H{"data": []GroupTransaction{}})
		return
	}

	// Fetch all splits for these transactions
	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, group_transaction_id, user_id, amount, transaction_id
		 FROM group_transaction_splits WHERE group_transaction_id = ANY($1)`, gtIDs)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	splitsByGT := make(map[uuid.UUID][]SplitDetail)
	for splitRows.Next() {
		var gtID uuid.UUID
		var s SplitDetail
		if err := splitRows.Scan(&s.ID, &gtID, &s.UserID, &s.Amount, &s.TransactionID); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		splitsByGT[gtID] = append(splitsByGT[gtID], s)
	}

	for i := range gts {
		if splits, ok := splitsByGT[gts[i].ID]; ok {
			gts[i].Splits = splits
		}
	}

	c.JSON(200, gin.H{"data": gts})
}

func GetGroupTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	txID, err := uuid.Parse(c.Param("txId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	var gt GroupTransaction
	var rawGTDate2, rawGTCreatedAt2, rawGTUpdatedAt2 time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at
		 FROM group_transactions WHERE id = $1 AND group_id = $2`,
		txID, groupID,
	).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawGTDate2,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawGTCreatedAt2, &rawGTUpdatedAt2,
	)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	gt.Date = helpers.FromTime(rawGTDate2)
	gt.CreatedAt = helpers.FromTime(rawGTCreatedAt2)
	gt.UpdatedAt = helpers.FromTime(rawGTUpdatedAt2)

	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, user_id, amount, transaction_id
		 FROM group_transaction_splits WHERE group_transaction_id = $1`, gt.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	gt.Splits = []SplitDetail{}
	for splitRows.Next() {
		var s SplitDetail
		if err := splitRows.Scan(&s.ID, &s.UserID, &s.Amount, &s.TransactionID); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		gt.Splits = append(gt.Splits, s)
	}

	c.JSON(200, gt)
}

func UpdateGroupTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	txID, err := uuid.Parse(c.Param("txId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Only the payer can update
	var paidByUserID uuid.UUID
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT paid_by_user_id FROM group_transactions WHERE id = $1 AND group_id = $2`,
		txID, groupID,
	).Scan(&paidByUserID)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	if paidByUserID != userID {
		c.JSON(403, gin.H{"error": "only the payer can update this transaction"})
		return
	}

	var req UpdateGroupTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	query := `UPDATE group_transactions SET updated_at = NOW()`
	args := []interface{}{}
	n := 1

	if req.Category != nil {
		query += fmt.Sprintf(", category = $%d", n)
		args = append(args, *req.Category)
		n++
	}
	if req.Date != nil {
		query += fmt.Sprintf(", date = $%d", n)
		args = append(args, time.UnixMilli(*req.Date).UTC())
		n++
	}
	if req.Description != nil {
		query += fmt.Sprintf(", description = $%d", n)
		args = append(args, *req.Description)
		n++
	}
	if req.Notes != nil {
		query += fmt.Sprintf(", notes = $%d", n)
		args = append(args, *req.Notes)
		n++
	}

	if n == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	query += fmt.Sprintf(` WHERE id = $%d
		RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`, n)
	args = append(args, txID)

	var gt GroupTransaction
	var rawGTDate3, rawGTCreatedAt3, rawGTUpdatedAt3 time.Time
	err = database.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawGTDate3,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawGTCreatedAt3, &rawGTUpdatedAt3,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update group transaction"})
		return
	}
	gt.Date = helpers.FromTime(rawGTDate3)
	gt.CreatedAt = helpers.FromTime(rawGTCreatedAt3)
	gt.UpdatedAt = helpers.FromTime(rawGTUpdatedAt3)

	splitRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, user_id, amount, transaction_id
		 FROM group_transaction_splits WHERE group_transaction_id = $1`, gt.ID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get splits"})
		return
	}
	defer splitRows.Close()

	gt.Splits = []SplitDetail{}
	for splitRows.Next() {
		var s SplitDetail
		if err := splitRows.Scan(&s.ID, &s.UserID, &s.Amount, &s.TransactionID); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan split"})
			return
		}
		gt.Splits = append(gt.Splits, s)
	}

	c.JSON(200, gt)
}

func DeleteGroupTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	txID, err := uuid.Parse(c.Param("txId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid transaction id"})
		return
	}

	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	var paidByUserID uuid.UUID
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT paid_by_user_id FROM group_transactions WHERE id = $1 AND group_id = $2`,
		txID, groupID,
	).Scan(&paidByUserID)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	if paidByUserID != userID {
		c.JSON(403, gin.H{"error": "only the payer can delete this transaction"})
		return
	}

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer dbTx.Rollback(c.Request.Context())

	// Soft-delete all linked personal transactions
	_, err = dbTx.Exec(c.Request.Context(),
		`UPDATE transactions SET is_deleted = true, updated_at = NOW()
		 WHERE group_transaction_id = $1`, txID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete personal transactions"})
		return
	}

	// Soft-delete the group transaction
	_, err = dbTx.Exec(c.Request.Context(),
		`UPDATE group_transactions SET is_deleted = true, updated_at = NOW() WHERE id = $1`, txID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete group transaction"})
		return
	}

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	c.JSON(200, gin.H{"message": "group transaction deleted successfully"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type payerEntry struct {
	paidBy uuid.UUID
	amount decimal.Decimal
}

type splitEntry struct {
	userID uuid.UUID
	amount decimal.Decimal
}

type settlementEntry struct {
	from, to uuid.UUID
	amount   decimal.Decimal
}

// computeBalances calculates net balance per member.
// Positive = owed money (paid more than share), Negative = owes money.
func computeBalances(members []GroupMember, payers []payerEntry, splits []splitEntry, settlements []settlementEntry) []Balance {
	bal := make(map[uuid.UUID]decimal.Decimal)
	for _, m := range members {
		bal[m.UserID] = decimal.Zero
	}

	for _, p := range payers {
		if _, ok := bal[p.paidBy]; ok {
			bal[p.paidBy] = bal[p.paidBy].Add(p.amount)
		}
	}

	for _, s := range splits {
		if _, ok := bal[s.userID]; ok {
			bal[s.userID] = bal[s.userID].Sub(s.amount)
		}
	}

	// Settlements: from_user paid their debt (+), to_user received (-)
	for _, s := range settlements {
		if _, ok := bal[s.from]; ok {
			bal[s.from] = bal[s.from].Add(s.amount)
		}
		if _, ok := bal[s.to]; ok {
			bal[s.to] = bal[s.to].Sub(s.amount)
		}
	}

	balances := []Balance{}
	for uid, amt := range bal {
		balances = append(balances, Balance{UserID: uid, Amount: amt.InexactFloat64()})
	}
	return balances
}
