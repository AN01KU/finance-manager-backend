package group

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

func CreateGroupTransaction(c *gin.Context, database *db.DB) {
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

	var req CreateGroupTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

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
		 WHERE group_transactions.group_id = EXCLUDED.group_id
		 RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`,
		gtID, groupID, req.PaidByUserID, totalAmount, req.Category, gtDate, req.Description, req.Notes,
	).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawGTDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawGTCreatedAt, &rawGTUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(409, gin.H{"error": "group transaction ID conflict with another group"})
			return
		}
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

	var total int
	if err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM group_transactions WHERE group_id = $1 AND is_deleted = FALSE`, groupID).Scan(&total); err != nil {
		c.JSON(500, gin.H{"error": "failed to get transaction count"})
		return
	}

	rows, err := database.Pool.Query(c.Request.Context(),
		`SELECT id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at
		 FROM group_transactions
		 WHERE group_id = $1 AND is_deleted = FALSE
		 ORDER BY date DESC, created_at DESC
		 LIMIT $2 OFFSET $3`, groupID, limit, offset)
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
		c.JSON(200, gin.H{
			"data":       []GroupTransaction{},
			"pagination": gin.H{"limit": limit, "offset": offset, "total": total},
		})
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

	c.JSON(200, gin.H{
		"data":       gts,
		"pagination": gin.H{"limit": limit, "offset": offset, "total": total},
	})
}

func GetGroupTransaction(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at
		 FROM group_transactions WHERE id = $1 AND group_id = $2`,
		txID, groupID,
	).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
	)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	gt.Date = helpers.FromTime(rawDate)
	gt.CreatedAt = helpers.FromTime(rawCreatedAt)
	gt.UpdatedAt = helpers.FromTime(rawUpdatedAt)

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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	gtQuery := `UPDATE group_transactions SET updated_at = NOW()`
	args := []interface{}{}
	n := 1

	// Track which personal-transaction fields need syncing
	personalQuery := `UPDATE transactions SET updated_at = NOW()`
	var personalArgs []interface{}
	pn := 1

	if req.Category != nil {
		gtQuery += fmt.Sprintf(", category = $%d", n)
		args = append(args, *req.Category)
		n++
		personalQuery += fmt.Sprintf(", category = $%d", pn)
		personalArgs = append(personalArgs, *req.Category)
		pn++
	}
	if req.Date != nil {
		t := time.UnixMilli(*req.Date).UTC()
		gtQuery += fmt.Sprintf(", date = $%d", n)
		args = append(args, t)
		n++
		personalQuery += fmt.Sprintf(", date = $%d", pn)
		personalArgs = append(personalArgs, t)
		pn++
	}
	if req.Description != nil {
		gtQuery += fmt.Sprintf(", description = $%d", n)
		args = append(args, *req.Description)
		n++
		personalQuery += fmt.Sprintf(", description = $%d", pn)
		personalArgs = append(personalArgs, *req.Description)
		pn++
	}
	if req.Notes != nil {
		gtQuery += fmt.Sprintf(", notes = $%d", n)
		args = append(args, *req.Notes)
		n++
		personalQuery += fmt.Sprintf(", notes = $%d", pn)
		personalArgs = append(personalArgs, *req.Notes)
		pn++
	}

	if n == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	gtQuery += fmt.Sprintf(` WHERE id = $%d
		RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`, n)
	args = append(args, txID)

	personalQuery += fmt.Sprintf(` WHERE group_transaction_id = $%d AND is_deleted = FALSE`, pn)
	personalArgs = append(personalArgs, txID)

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer dbTx.Rollback(c.Request.Context())

	var gt GroupTransaction
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time
	err = dbTx.QueryRow(c.Request.Context(), gtQuery, args...).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update group transaction"})
		return
	}
	gt.Date = helpers.FromTime(rawDate)
	gt.CreatedAt = helpers.FromTime(rawCreatedAt)
	gt.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	// Sync the same field changes to linked personal transactions.
	if _, err = dbTx.Exec(c.Request.Context(), personalQuery, personalArgs...); err != nil {
		c.JSON(500, gin.H{"error": "failed to sync personal transactions"})
		return
	}

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

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
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
