package group

import (
	"context"
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

	// Validate paid_by + every split user is a group member in ONE query.
	toCheck := make([]uuid.UUID, 0, len(req.Splits)+1)
	toCheck = append(toCheck, req.PaidByUserID)
	for _, s := range req.Splits {
		toCheck = append(toCheck, s.UserID)
	}
	missing, err := helpers.MissingGroupMembers(c.Request.Context(), database, groupID, toCheck)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to validate group members"})
		return
	}
	if len(missing) > 0 {
		// Surface the first missing user for actionable error; clients can re-query if they need more.
		if missing[0] == req.PaidByUserID {
			c.JSON(400, gin.H{"error": "paid_by_user_id is not a member of the group"})
		} else {
			c.JSON(400, gin.H{"error": fmt.Sprintf("user %s is not a member of the group", missing[0])})
		}
		return
	}

	// Mixed-currency reject: every user involved in the transaction (payer +
	// every split member) must share the same users.currency. Without this,
	// totals and balances silently mis-sum. Multi-currency support is R2.
	var distinctCurrencies int
	if err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT currency) FROM users WHERE id = ANY($1)`,
		toCheck,
	).Scan(&distinctCurrencies); err != nil {
		c.JSON(500, gin.H{"error": "failed to validate currencies"})
		return
	}
	if distinctCurrencies > 1 {
		c.JSON(400, gin.H{"error": "group transaction users have different currencies", "code": "MIXED_CURRENCY_GROUP_TX"})
		return
	}

	resolvedCategory, err := helpers.ResolveCategoryKey(c.Request.Context(), database.Pool, userID, req.Category)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to resolve category"})
		return
	}

	gtDate := time.UnixMilli(req.Date).UTC()

	gtID := uuid.New()
	if req.ID != nil {
		gtID = *req.ID
	}

	// LWW: prefer the client-supplied updated_at when present so a stale device
	// can be detected against a newer server-side row. Fallback to NOW() when
	// absent (legacy behavior; no LWW protection but never blocks).
	updatedAt := time.Now()
	if req.UpdatedAt != nil {
		updatedAt = time.UnixMilli(*req.UpdatedAt).UTC()
	}

	tx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	// Upsert group_transaction. The WHERE clause on ON CONFLICT defends against
	// cross-group UUID collisions: if the row exists but belongs to a different
	// group, the WHERE filters it out, no row is updated, RETURNING is empty,
	// and we surface a 409. (Without this, a client could silently overwrite
	// another group's transaction by reusing its UUID.)
	var gt GroupTransaction
	var rawGTDate, rawGTCreatedAt, rawGTUpdatedAt time.Time
	err = tx.QueryRow(c.Request.Context(),
		`INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, description, notes, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (id) DO UPDATE SET
		   paid_by_user_id = EXCLUDED.paid_by_user_id,
		   total_amount = EXCLUDED.total_amount,
		   category = EXCLUDED.category,
		   date = EXCLUDED.date,
		   description = EXCLUDED.description,
		   notes = EXCLUDED.notes,
		   is_deleted = FALSE,
		   updated_at = EXCLUDED.updated_at
		 WHERE group_transactions.group_id = EXCLUDED.group_id
		   AND group_transactions.updated_at <= EXCLUDED.updated_at
		 RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`,
		gtID, groupID, req.PaidByUserID, totalAmount, resolvedCategory, gtDate, req.Description, req.Notes, updatedAt,
	).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawGTDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawGTCreatedAt, &rawGTUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish: row exists but the WHERE rejected the update.
			// Either (a) UUID belongs to another group, or (b) the existing
			// row is newer than this incoming write (stale write). Stale
			// reject = full no-op (splits untouched, since we return before
			// applySplitsInPlace).
			var existingGroupID uuid.UUID
			var existingUpdatedAt time.Time
			lookupErr := database.Pool.QueryRow(c.Request.Context(),
				`SELECT group_id, updated_at FROM group_transactions WHERE id = $1`, gtID,
			).Scan(&existingGroupID, &existingUpdatedAt)
			if lookupErr == nil {
				if existingGroupID != groupID {
					c.JSON(409, gin.H{"error": "group transaction ID already exists in another group", "code": "ID_OWNED_BY_ANOTHER_GROUP"})
					return
				}
				if existingUpdatedAt.After(updatedAt) {
					c.JSON(409, gin.H{"error": "stale write: a newer version exists on the server", "code": "STALE_WRITE"})
					return
				}
			}
			c.JSON(409, gin.H{"error": "group transaction ID conflict"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to create group transaction"})
		return
	}
	gt.Date = helpers.FromTime(rawGTDate)
	gt.CreatedAt = helpers.FromTime(rawGTCreatedAt)
	gt.UpdatedAt = helpers.FromTime(rawGTUpdatedAt)

	// Apply splits in-place. On first insert this is just N inserts; on
	// idempotent retry (same id, same group) it becomes upsert + soft-remove,
	// preserving personal transaction IDs across re-syncs.
	splits, err := applySplitsInPlace(c.Request.Context(), tx, gt.ID, req.Splits, splitAmounts, resolvedCategory, gtDate, req.Description, req.Notes)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	gt.Splits = splits
	c.JSON(201, gt)
}

// applySplitsInPlace reconciles the desired set of splits with what currently
// exists in the DB for `gtID`, preserving transaction IDs wherever possible.
//
// For each split user:
//   - If a split already exists → UPDATE the personal transaction (amount + meta)
//     and the split row.
//   - If no split exists → INSERT a new personal transaction + split row.
//
// For users present in DB but not in `desiredSplits`:
//   - Soft-delete the personal transaction and DELETE the split row.
//
// This replaces the previous "soft-delete all + recreate all" strategy, which
// burned through transaction IDs on every edit and broke any external link
// (sync clients, exports, notifications) to the personal rows.
func applySplitsInPlace(
	ctx context.Context,
	tx pgx.Tx,
	gtID uuid.UUID,
	desiredSplits []SplitInput,
	desiredAmounts []decimal.Decimal,
	category string,
	date time.Time,
	description, notes *string,
) ([]SplitDetail, error) {
	// Snapshot existing splits keyed by user_id.
	type existing struct {
		splitID uuid.UUID
		txID    *uuid.UUID
	}
	rows, err := tx.Query(ctx,
		`SELECT id, user_id, transaction_id
		 FROM group_transaction_splits WHERE group_transaction_id = $1`, gtID)
	if err != nil {
		return nil, fmt.Errorf("load existing splits: %w", err)
	}
	current := map[uuid.UUID]existing{}
	for rows.Next() {
		var sid, uid uuid.UUID
		var txID *uuid.UUID
		if err := rows.Scan(&sid, &uid, &txID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan existing split: %w", err)
		}
		current[uid] = existing{splitID: sid, txID: txID}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate splits: %w", err)
	}

	desiredByUser := make(map[uuid.UUID]struct{}, len(desiredSplits))
	for _, s := range desiredSplits {
		desiredByUser[s.UserID] = struct{}{}
	}

	// Step 1: remove users no longer in splits — soft-delete personal txs +
	// delete split rows. Soft delete keeps the cleanup worker's invariants.
	for uid, ex := range current {
		if _, keep := desiredByUser[uid]; keep {
			continue
		}
		if ex.txID != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE transactions SET is_deleted = TRUE, updated_at = NOW()
				 WHERE id = $1 AND is_deleted = FALSE`, *ex.txID); err != nil {
				return nil, fmt.Errorf("soft-delete removed-member tx: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM group_transaction_splits WHERE id = $1`, ex.splitID); err != nil {
			return nil, fmt.Errorf("delete removed-member split: %w", err)
		}
	}

	// Step 2: upsert every desired split + its personal transaction.
	out := make([]SplitDetail, 0, len(desiredSplits))
	for i, s := range desiredSplits {
		amt := desiredAmounts[i]

		ex, hadExisting := current[s.UserID]

		var memberTxID uuid.UUID
		if hadExisting && ex.txID != nil {
			memberTxID = *ex.txID
			// Update existing personal transaction in place. is_deleted=FALSE
			// in case it was soft-deleted previously (e.g. by an older edit
			// path) and the user is being re-added.
			if _, err := tx.Exec(ctx,
				`UPDATE transactions
				 SET amount = $1, category = $2, date = $3,
				     description = $4, notes = $5, is_deleted = FALSE, updated_at = NOW()
				 WHERE id = $6`,
				amt, category, date, description, notes, memberTxID,
			); err != nil {
				return nil, fmt.Errorf("update personal tx for %s: %w", s.UserID, err)
			}
		} else {
			// New member: insert a fresh personal transaction.
			if err := tx.QueryRow(ctx,
				`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_transaction_id, updated_at)
				 VALUES ($1, 'expense', $2, $3, $4, $5, $6, $7, NOW())
				 RETURNING id`,
				s.UserID, amt, category, date, description, notes, gtID,
			).Scan(&memberTxID); err != nil {
				return nil, fmt.Errorf("insert personal tx for %s: %w", s.UserID, err)
			}
		}

		var split SplitDetail
		if hadExisting {
			// Update the existing split row's amount + (re-)bind to the personal tx.
			if err := tx.QueryRow(ctx,
				`UPDATE group_transaction_splits
				 SET amount = $1, transaction_id = $2
				 WHERE id = $3
				 RETURNING id, user_id, amount, transaction_id`,
				amt, memberTxID, ex.splitID,
			).Scan(&split.ID, &split.UserID, &split.Amount, &split.TransactionID); err != nil {
				return nil, fmt.Errorf("update split for %s: %w", s.UserID, err)
			}
		} else {
			if err := tx.QueryRow(ctx,
				`INSERT INTO group_transaction_splits (group_transaction_id, user_id, amount, transaction_id)
				 VALUES ($1, $2, $3, $4)
				 RETURNING id, user_id, amount, transaction_id`,
				gtID, s.UserID, amt, memberTxID,
			).Scan(&split.ID, &split.UserID, &split.Amount, &split.TransactionID); err != nil {
				return nil, fmt.Errorf("insert split for %s: %w", s.UserID, err)
			}
		}
		out = append(out, split)
	}

	return out, nil
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
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := splitRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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
	if err := splitRows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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

	// Authorization: anyone involved (payer OR any split member) can update.
	// This matches Delete semantics and prevents "immortal transaction" where
	// the original payer leaves the group.
	var currentPaidByUserID uuid.UUID
	var isInvolved bool
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT gt.paid_by_user_id,
		        gt.paid_by_user_id = $3
		        OR EXISTS (SELECT 1 FROM group_transaction_splits s
		                   WHERE s.group_transaction_id = gt.id AND s.user_id = $3) AS involved
		 FROM group_transactions gt
		 WHERE gt.id = $1 AND gt.group_id = $2 AND gt.is_deleted = FALSE`,
		txID, groupID, userID,
	).Scan(&currentPaidByUserID, &isInvolved)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	if !isInvolved {
		c.JSON(403, gin.H{"error": "only the payer or someone in the splits can update this transaction"})
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

	// LWW: client may supply the version timestamp it's editing from.
	updatedAt := time.Now()
	if req.UpdatedAt != nil {
		updatedAt = time.UnixMilli(*req.UpdatedAt).UTC()
	}

	// Resolve the effective payer after this update — used for member + currency checks below.
	effectivePayer := currentPaidByUserID
	if req.PaidByUserID != nil {
		effectivePayer = *req.PaidByUserID
	}

	gtQuery := `UPDATE group_transactions SET updated_at = $1`
	args := []interface{}{updatedAt}
	n := 2 // $1 is updatedAt

	// Track which personal-transaction fields need syncing
	personalQuery := `UPDATE transactions SET updated_at = NOW()`
	var personalArgs []interface{}
	pn := 1

	if req.PaidByUserID != nil {
		// Validate new payer is a current group member.
		isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, *req.PaidByUserID)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to validate new payer membership"})
			return
		}
		if !isMember {
			c.JSON(400, gin.H{"error": "paid_by_user_id is not a member of the group"})
			return
		}
		gtQuery += fmt.Sprintf(", paid_by_user_id = $%d", n)
		args = append(args, *req.PaidByUserID)
		n++
	}
	if req.Category != nil {
		resolvedCat, err := helpers.ResolveCategoryKey(c.Request.Context(), database.Pool, userID, *req.Category)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to resolve category"})
			return
		}
		gtQuery += fmt.Sprintf(", category = $%d", n)
		args = append(args, resolvedCat)
		n++
		personalQuery += fmt.Sprintf(", category = $%d", pn)
		personalArgs = append(personalArgs, resolvedCat)
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

	// Validate total_amount / splits combination
	hasSplits := len(req.Splits) > 0
	var newTotalAmount decimal.Decimal
	var splitAmounts []decimal.Decimal

	if req.TotalAmount != nil {
		newTotalAmount = decimal.NewFromFloat(*req.TotalAmount)
		if newTotalAmount.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "invalid total_amount"})
			return
		}
		if !hasSplits {
			c.JSON(400, gin.H{"error": "splits must be provided when changing total_amount"})
			return
		}
		gtQuery += fmt.Sprintf(", total_amount = $%d", n)
		args = append(args, newTotalAmount)
		n++
	}

	if hasSplits {
		// If TotalAmount not provided, fetch the current one
		if req.TotalAmount == nil {
			var currentTotal float64
			err = database.Pool.QueryRow(c.Request.Context(),
				`SELECT total_amount FROM group_transactions WHERE id = $1 AND group_id = $2`,
				txID, groupID,
			).Scan(&currentTotal)
			if err != nil {
				c.JSON(404, gin.H{"error": "group transaction not found"})
				return
			}
			newTotalAmount = decimal.NewFromFloat(currentTotal)
		}

		// Validate split amounts
		splitAmounts = make([]decimal.Decimal, len(req.Splits))
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

		if !splitSum.Equal(newTotalAmount) {
			c.JSON(400, gin.H{"error": fmt.Sprintf("splits sum (%s) must equal total_amount (%s)", splitSum, newTotalAmount)})
			return
		}

		// Validate all split users are group members in one query.
		uids := make([]uuid.UUID, 0, len(req.Splits))
		for _, s := range req.Splits {
			uids = append(uids, s.UserID)
		}
		missing, err := helpers.MissingGroupMembers(c.Request.Context(), database, groupID, uids)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to validate group members"})
			return
		}
		if len(missing) > 0 {
			c.JSON(400, gin.H{"error": fmt.Sprintf("user %s is not a member of the group", missing[0])})
			return
		}

		// Mixed-currency reject: effective payer + all split members must share
		// the same users.currency. Multi-currency support is R2.
		ccCheck := append([]uuid.UUID{effectivePayer}, uids...)
		var distinctCurrencies int
		if err := database.Pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(DISTINCT currency) FROM users WHERE id = ANY($1)`,
			ccCheck,
		).Scan(&distinctCurrencies); err != nil {
			c.JSON(500, gin.H{"error": "failed to validate currencies"})
			return
		}
		if distinctCurrencies > 1 {
			c.JSON(400, gin.H{"error": "group transaction users have different currencies", "code": "MIXED_CURRENCY_GROUP_TX"})
			return
		}
	} else if req.PaidByUserID != nil {
		// Payer change without splits: re-check currency against existing split members.
		var distinctCurrencies int
		if err := database.Pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(DISTINCT u.currency)
			 FROM users u
			 WHERE u.id = $1
			    OR u.id IN (SELECT user_id FROM group_transaction_splits WHERE group_transaction_id = $2)`,
			effectivePayer, txID,
		).Scan(&distinctCurrencies); err != nil {
			c.JSON(500, gin.H{"error": "failed to validate currencies"})
			return
		}
		if distinctCurrencies > 1 {
			c.JSON(400, gin.H{"error": "group transaction users have different currencies", "code": "MIXED_CURRENCY_GROUP_TX"})
			return
		}
	}

	if n == 2 && !hasSplits {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	// LWW guard: reject if server row is newer than client's version.
	// On conflict: follow-up SELECT distinguishes 404 vs STALE_WRITE.
	gtQuery += fmt.Sprintf(` WHERE id = $%d AND updated_at <= $1
		RETURNING id, group_id, paid_by_user_id, total_amount, category, date, description, notes, is_deleted, created_at, updated_at`, n)
	args = append(args, txID)

	personalQuery += fmt.Sprintf(` WHERE group_transaction_id = $%d AND is_deleted = FALSE`, pn)
	personalArgs = append(personalArgs, txID)

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer func() { _ = dbTx.Rollback(c.Request.Context()) }()

	var gt GroupTransaction
	var rawDate, rawCreatedAt, rawUpdatedAt time.Time

	err = dbTx.QueryRow(c.Request.Context(), gtQuery, args...).Scan(
		&gt.ID, &gt.GroupID, &gt.PaidByUserID, &gt.TotalAmount, &gt.Category, &rawDate,
		&gt.Description, &gt.Notes, &gt.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish 404 vs stale write via follow-up SELECT.
			var existingUpdatedAt time.Time
			lookupErr := database.Pool.QueryRow(c.Request.Context(),
				`SELECT updated_at FROM group_transactions WHERE id = $1 AND group_id = $2 AND is_deleted = FALSE`,
				txID, groupID,
			).Scan(&existingUpdatedAt)
			if lookupErr == nil && req.UpdatedAt != nil && existingUpdatedAt.After(updatedAt) {
				c.JSON(409, gin.H{"error": "stale write: a newer version exists on the server", "code": "STALE_WRITE"})
				return
			}
			c.JSON(404, gin.H{"error": "group transaction not found"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to update group transaction"})
		return
	}
	gt.Date = helpers.FromTime(rawDate)
	gt.CreatedAt = helpers.FromTime(rawCreatedAt)
	gt.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	if hasSplits {
		// Reconcile in place — preserve personal transaction IDs for unchanged
		// members; insert for new members; soft-delete + remove for removed.
		splits, err := applySplitsInPlace(c.Request.Context(), dbTx, gt.ID, req.Splits, splitAmounts, gt.Category, rawDate, gt.Description, gt.Notes)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		gt.Splits = splits
	} else {
		// Sync field changes to linked personal transactions.
		if len(personalArgs) > 1 {
			if _, err = dbTx.Exec(c.Request.Context(), personalQuery, personalArgs...); err != nil {
				c.JSON(500, gin.H{"error": "failed to sync personal transactions"})
				return
			}
		}
	}

	// When paid_by_user_id changes, the old payer's personal expense row (which
	// is keyed by group_transaction_id but NOT by paid_by_user_id) stays owned
	// by whoever holds the split for that user. There is no separate "payer
	// personal tx" — the payer's personal expense is the split row for their
	// share. No additional sync needed beyond what applySplitsInPlace handles.

	if err := dbTx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit"})
		return
	}

	// If splits weren't replaced, fetch them
	if !hasSplits {
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
		if err := splitRows.Err(); err != nil {
			c.JSON(500, gin.H{"error": "database iteration failed"})
			return
		}
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

	// Authorization: anyone involved in the transaction (payer OR any split
	// member) can delete. This prevents the "immortal transaction" case where
	// the original payer leaves the group and nobody else can clean it up.
	var paidByUserID uuid.UUID
	var isInvolved bool
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT gt.paid_by_user_id,
		        gt.paid_by_user_id = $3
		        OR EXISTS (SELECT 1 FROM group_transaction_splits s
		                   WHERE s.group_transaction_id = gt.id AND s.user_id = $3) AS involved
		 FROM group_transactions gt
		 WHERE gt.id = $1 AND gt.group_id = $2`,
		txID, groupID, userID,
	).Scan(&paidByUserID, &isInvolved)
	if err != nil {
		c.JSON(404, gin.H{"error": "group transaction not found"})
		return
	}
	if !isInvolved {
		c.JSON(403, gin.H{"error": "only the payer or someone in the splits can delete this transaction"})
		return
	}

	dbTx, err := database.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to start transaction"})
		return
	}
	defer func() { _ = dbTx.Rollback(c.Request.Context()) }()

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
