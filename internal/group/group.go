package group

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

var validate = validator.New()

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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

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
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create group"})
		return
	}
	g.CreatedAt = helpers.FromTime(rawGroupCreatedAt)

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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := memberRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := gtRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := splitRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := settRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid group id"})
		return
	}

	var g Group
	var rawCreatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		"SELECT id, name, created_by, created_at FROM groups WHERE id = $1", groupID,
	).Scan(&g.ID, &g.Name, &g.CreatedBy, &rawCreatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "group not found"})
		return
	}
	g.CreatedAt = helpers.FromTime(rawCreatedAt)

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

	// Fetch payer and split data in a single query using UNION ALL.
	balanceRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT 'payer' AS kind, paid_by_user_id, total_amount
		   FROM group_transactions WHERE group_id = $1 AND is_deleted = FALSE
		 UNION ALL
		 SELECT 'split', gts.user_id, gts.amount
		   FROM group_transaction_splits gts
		   JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		   WHERE gt.group_id = $1 AND gt.is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get balance data"})
		return
	}
	defer balanceRows.Close()

	var payers []payerEntry
	var splits []splitEntry
	for balanceRows.Next() {
		var kind string
		var uid uuid.UUID
		var amount decimal.Decimal
		if err := balanceRows.Scan(&kind, &uid, &amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan balance data"})
			return
		}
		if kind == "payer" {
			payers = append(payers, payerEntry{paidBy: uid, amount: amount})
		} else {
			splits = append(splits, splitEntry{userID: uid, amount: amount})
		}
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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}

	c.JSON(200, gin.H{"data": members})
}

func GetBalances(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	if err := memberRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}

	balanceRows, err := database.Pool.Query(c.Request.Context(),
		`SELECT 'payer' AS kind, paid_by_user_id, total_amount
		   FROM group_transactions WHERE group_id = $1 AND is_deleted = FALSE
		 UNION ALL
		 SELECT 'split', gts.user_id, gts.amount
		   FROM group_transaction_splits gts
		   JOIN group_transactions gt ON gts.group_transaction_id = gt.id
		   WHERE gt.group_id = $1 AND gt.is_deleted = FALSE`, groupID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get balance data"})
		return
	}
	defer balanceRows.Close()

	var payers []payerEntry
	var splits []splitEntry
	for balanceRows.Next() {
		var kind string
		var uid uuid.UUID
		var amount decimal.Decimal
		if err := balanceRows.Scan(&kind, &uid, &amount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan balance data"})
			return
		}
		if kind == "payer" {
			payers = append(payers, payerEntry{paidBy: uid, amount: amount})
		} else {
			splits = append(splits, splitEntry{userID: uid, amount: amount})
		}
	}
	if err := balanceRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := settRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}

	balances := computeBalances(members, payers, splits, setts)
	c.JSON(200, gin.H{"data": balances})
}

func GetGroupSettlements(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
