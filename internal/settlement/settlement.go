package settlement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

// httpError is a sentinel that carries an HTTP status code and response body
// through a WithTx closure so the handler can write the correct response after
// the transaction is rolled back.
type httpError struct {
	status int
	body   gin.H
}

func (e *httpError) Error() string { return fmt.Sprintf("http %d", e.status) }

// pairwiseQueryable is the subset of pgx that pairwiseDebt needs. Both
// *pgxpool.Pool and pgx.Tx satisfy it, so the helper can run inside or
// outside a transaction.
type pairwiseQueryable interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pairwiseDebt returns how much `fromUser` owes `toUser` in `groupID` BEFORE
// applying the settlement under consideration. Positive = fromUser owes;
// negative = toUser owes fromUser; zero = even.
//
// excludeSettlement, if non-nil, is excluded from the prior-settlements sums
// (used during UpdateSettlement so the row being edited doesn't double-count
// against its own new amount).
//
// Components:
//   - fromUser's split share in transactions paid by toUser  (fromUser owes)
//   - toUser's   split share in transactions paid by fromUser (toUser owes back)
//   - prior settlements fromUser → toUser   (already paid down debt)
//   - prior settlements toUser   → fromUser (added to fromUser's claim)
func pairwiseDebt(ctx context.Context, q pairwiseQueryable, groupID, fromUser, toUser uuid.UUID, excludeSettlement *uuid.UUID) (decimal.Decimal, error) {
	excludeID := uuid.Nil
	if excludeSettlement != nil {
		excludeID = *excludeSettlement
	}

	var debt decimal.Decimal
	err := q.QueryRow(ctx,
		`WITH from_owes AS (
		   SELECT COALESCE(SUM(s.amount), 0)::numeric AS amt
		   FROM group_transaction_splits s
		   JOIN group_transactions gt ON s.group_transaction_id = gt.id
		   WHERE gt.group_id = $1 AND gt.is_deleted = FALSE
		     AND s.user_id = $2 AND gt.paid_by_user_id = $3
		 ),
		 to_owes AS (
		   SELECT COALESCE(SUM(s.amount), 0)::numeric AS amt
		   FROM group_transaction_splits s
		   JOIN group_transactions gt ON s.group_transaction_id = gt.id
		   WHERE gt.group_id = $1 AND gt.is_deleted = FALSE
		     AND s.user_id = $3 AND gt.paid_by_user_id = $2
		 ),
		 prior_ft AS (
		   SELECT COALESCE(SUM(amount), 0)::numeric AS amt
		   FROM settlements
		   WHERE group_id = $1 AND from_user = $2 AND to_user = $3
		     AND is_deleted = FALSE AND id != $4
		 ),
		 prior_tf AS (
		   SELECT COALESCE(SUM(amount), 0)::numeric AS amt
		   FROM settlements
		   WHERE group_id = $1 AND from_user = $3 AND to_user = $2
		     AND is_deleted = FALSE AND id != $4
		 )
		 SELECT (from_owes.amt - to_owes.amt - prior_ft.amt + prior_tf.amt)
		 FROM from_owes, to_owes, prior_ft, prior_tf`,
		groupID, fromUser, toUser, excludeID,
	).Scan(&debt)
	return debt, err
}

var validate = validator.New()

type Settlement struct {
	ID        uuid.UUID           `json:"id"`
	GroupID   uuid.UUID           `json:"group_id"`
	FromUser  *uuid.UUID          `json:"from_user"`
	ToUser    *uuid.UUID          `json:"to_user"`
	Amount    float64             `json:"amount"`
	Notes     *string             `json:"notes,omitempty"`
	IsDeleted bool                `json:"is_deleted"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
	UpdatedAt helpers.EpochMillis `json:"updated_at"`
}

type UpdateSettlementRequest struct {
	Amount *float64 `json:"amount,omitempty"`
	Notes  *string  `json:"notes,omitempty"`
}

type CreateSettlementRequest struct {
	GroupID  uuid.UUID `json:"group_id" validate:"required"`
	FromUser uuid.UUID `json:"from_user" validate:"required"`
	ToUser   uuid.UUID `json:"to_user" validate:"required"`
	Amount   float64   `json:"amount" validate:"required"`
	Notes    *string   `json:"notes,omitempty"`
}

func CreateSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	groupID := req.GroupID

	// Check if user is member of group
	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, groupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	// Check from_user and to_user are members
	isMember, err = helpers.IsGroupMember(c.Request.Context(), database, groupID, req.FromUser)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "from_user is not a member of the group"})
		return
	}
	isMember, err = helpers.IsGroupMember(c.Request.Context(), database, groupID, req.ToUser)
	if err != nil || !isMember {
		c.JSON(400, gin.H{"error": "to_user is not a member of the group"})
		return
	}

	if req.FromUser == req.ToUser {
		c.JSON(400, gin.H{"error": "cannot settle to self"})
		return
	}

	// The requesting user must be a party to the settlement
	if userID != req.FromUser && userID != req.ToUser {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	amount := decimal.NewFromFloat(req.Amount)

	if amount.LessThanOrEqual(decimal.Zero) {
		c.JSON(400, gin.H{"error": "amount must be greater than 0"})
		return
	}

	// Mixed-currency reject: from_user and to_user must share the same
	// users.currency. Otherwise pairwiseDebt sums mismatched amounts and
	// the excess-portion personal txs get booked in arbitrary currencies.
	// Multi-currency support is R2.
	var distinctCurrencies int
	if err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT currency) FROM users WHERE id IN ($1, $2)`,
		req.FromUser, req.ToUser,
	).Scan(&distinctCurrencies); err != nil {
		c.JSON(500, gin.H{"error": "failed to validate currencies"})
		return
	}
	if distinctCurrencies > 1 {
		c.JSON(400, gin.H{"error": "settlement parties have different currencies", "code": "MIXED_CURRENCY_SETTLEMENT"})
		return
	}

	// Use a DB transaction so settlement + personal transactions are atomic.
	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	if err := db.WithTx(c.Request.Context(), database.Pool, func(dbTx pgx.Tx) error {
		err = dbTx.QueryRow(c.Request.Context(),
			"INSERT INTO settlements (group_id, from_user, to_user, amount, notes) VALUES ($1, $2, $3, $4, $5) RETURNING id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at",
			groupID, req.FromUser, req.ToUser, amount, req.Notes).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
		if err != nil {
			return err
		}
		s.CreatedAt = helpers.FromTime(rawCreatedAt)
		s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

		// Compute pairwise debt BEFORE this settlement and only book the excess
		// portion as personal transactions. The "covered" portion (≤ existing
		// debt) is already represented as expense rows in the original group
		// transactions — booking it again would double-count in dashboards.
		//
		//   amount ≤ debt  → no rows (pure debt clearing)
		//   amount > debt  → expense for from_user / income for to_user, equal
		//                    to (amount − max(0, debt)). Linked via settlement_id
		//                    so DeleteSettlement cascades cleanly.
		debt, err := pairwiseDebt(c.Request.Context(), dbTx, groupID, req.FromUser, req.ToUser, nil)
		if err != nil {
			return err
		}
		effect := ComputeSettlementEffect(debt, amount)
		if effect.Excess.IsPositive() {
			if _, err = dbTx.Exec(c.Request.Context(),
				`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
				 VALUES ($1, 'income', $2, 'other', NOW(), $3, $4, $5, $6)`,
				req.ToUser, effect.Excess, "Settlement excess received", req.Notes, groupID, s.ID); err != nil {
				return err
			}
			if _, err = dbTx.Exec(c.Request.Context(),
				`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
				 VALUES ($1, 'expense', $2, 'other', NOW(), $3, $4, $5, $6)`,
				req.FromUser, effect.Excess, "Settlement excess paid", req.Notes, groupID, s.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		c.JSON(500, gin.H{"error": "failed to create settlement"})
		return
	}

	c.JSON(201, s)
}

func GetSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	// Only parties to the settlement or group members can view it
	isMember, err := helpers.IsGroupMember(c.Request.Context(), database, s.GroupID, userID)
	if err != nil || !isMember {
		c.JSON(403, gin.H{"error": "not a member of the group"})
		return
	}

	s.CreatedAt = helpers.FromTime(rawCreatedAt)
	s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, s)
}

func DeleteSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	isParty := (s.FromUser != nil && userID == *s.FromUser) || (s.ToUser != nil && userID == *s.ToUser)
	if !isParty {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	if err := db.WithTx(c.Request.Context(), database.Pool, func(dbTx pgx.Tx) error {
		if _, err := dbTx.Exec(c.Request.Context(),
			`UPDATE transactions SET is_deleted = true, updated_at = NOW() WHERE settlement_id = $1`, id); err != nil {
			return err
		}
		_, err := dbTx.Exec(c.Request.Context(),
			`UPDATE settlements SET is_deleted = true, updated_at = NOW() WHERE id = $1`, id)
		return err
	}); err != nil {
		c.JSON(500, gin.H{"error": "failed to delete settlement"})
		return
	}

	c.Status(204)
}

func UpdateSettlement(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid settlement id"})
		return
	}

	var s Settlement
	var rawCreatedAt, rawUpdatedAt time.Time
	err = database.Pool.QueryRow(c.Request.Context(),
		`SELECT id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at
		 FROM settlements
		 WHERE id = $1 AND is_deleted = FALSE`, id,
	).Scan(&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "settlement not found"})
		return
	}

	isParty2 := (s.FromUser != nil && userID == *s.FromUser) || (s.ToUser != nil && userID == *s.ToUser)
	if !isParty2 {
		c.JSON(403, gin.H{"error": "you must be either the payer or the recipient"})
		return
	}

	var req UpdateSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	settleQuery := `UPDATE settlements SET updated_at = NOW()`
	args := []interface{}{}
	n := 1

	var newAmount *decimal.Decimal
	if req.Amount != nil {
		amount := decimal.NewFromFloat(*req.Amount)
		if amount.LessThanOrEqual(decimal.Zero) {
			c.JSON(400, gin.H{"error": "amount must be greater than 0"})
			return
		}
		newAmount = &amount
		settleQuery += fmt.Sprintf(", amount = $%d", n)
		args = append(args, amount)
		n++
	}
	if req.Notes != nil {
		settleQuery += fmt.Sprintf(", notes = $%d", n)
		args = append(args, *req.Notes)
		n++
	}

	if n == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	settleQuery += fmt.Sprintf(` WHERE id = $%d
		RETURNING id, group_id, from_user, to_user, amount, notes, is_deleted, created_at, updated_at`, n)
	args = append(args, id)

	txErr := db.WithTx(c.Request.Context(), database.Pool, func(dbTx pgx.Tx) error {
		if err := dbTx.QueryRow(c.Request.Context(), settleQuery, args...).Scan(
			&s.ID, &s.GroupID, &s.FromUser, &s.ToUser, &s.Amount, &s.Notes, &s.IsDeleted, &rawCreatedAt, &rawUpdatedAt,
		); err != nil {
			return err
		}
		s.CreatedAt = helpers.FromTime(rawCreatedAt)
		s.UpdatedAt = helpers.FromTime(rawUpdatedAt)

		// If amount changed, the excess portion may have changed too. Drop any
		// existing settlement-linked tx pair and recreate it for the new excess.
		// Hard-DELETE here (instead of soft-delete) because these rows are
		// server-managed and have no externally-referenced IDs — they only ever
		// existed as a side effect of this settlement.
		if newAmount != nil {
			// If either party was deleted (NULL), we can't recompute excess — skip.
			if s.FromUser == nil || s.ToUser == nil {
				return &httpError{409, gin.H{"error": "cannot update amount: one or more parties have been deleted"}}
			}

			// Re-validate currencies in case a party changed users.currency
			// between create and update — guards the same invariant as Create.
			var distinctCurrencies int
			if err := dbTx.QueryRow(c.Request.Context(),
				`SELECT COUNT(DISTINCT currency) FROM users WHERE id IN ($1, $2)`,
				*s.FromUser, *s.ToUser,
			).Scan(&distinctCurrencies); err != nil {
				return err
			}
			if distinctCurrencies > 1 {
				return &httpError{400, gin.H{"error": "settlement parties have different currencies", "code": "MIXED_CURRENCY_SETTLEMENT"}}
			}

			if _, err := dbTx.Exec(c.Request.Context(),
				`DELETE FROM transactions WHERE settlement_id = $1`, id); err != nil {
				return err
			}

			debt, err := pairwiseDebt(c.Request.Context(), dbTx, s.GroupID, *s.FromUser, *s.ToUser, &id)
			if err != nil {
				return err
			}
			effect := ComputeSettlementEffect(debt, *newAmount)
			if effect.Excess.IsPositive() {
				if _, err = dbTx.Exec(c.Request.Context(),
					`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
					 VALUES ($1, 'income', $2, 'other', NOW(), $3, $4, $5, $6)`,
					*s.ToUser, effect.Excess, "Settlement excess received", s.Notes, s.GroupID, s.ID); err != nil {
					return err
				}
				if _, err = dbTx.Exec(c.Request.Context(),
					`INSERT INTO transactions (user_id, type, amount, category, date, description, notes, group_id, settlement_id)
					 VALUES ($1, 'expense', $2, 'other', NOW(), $3, $4, $5, $6)`,
					*s.FromUser, effect.Excess, "Settlement excess paid", s.Notes, s.GroupID, s.ID); err != nil {
					return err
				}
			}
		} else if req.Notes != nil {
			// Amount unchanged but notes changed — sync notes onto whatever
			// linked rows exist (there will be 0 or 2).
			if _, err := dbTx.Exec(c.Request.Context(),
				`UPDATE transactions SET notes = $1, updated_at = NOW()
				 WHERE settlement_id = $2 AND is_deleted = FALSE`,
				*req.Notes, id,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		var he *httpError
		if errors.As(txErr, &he) {
			c.JSON(he.status, he.body)
			return
		}
		c.JSON(500, gin.H{"error": "failed to update settlement"})
		return
	}

	c.JSON(200, s)
}
