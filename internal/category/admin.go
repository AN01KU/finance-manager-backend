package category

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
)

// AdminPredefinedCategory is the admin-facing shape that exposes is_hidden
// in addition to the public fields.
type AdminPredefinedCategory struct {
	ID        uuid.UUID           `json:"id"`
	Key       string              `json:"key"`
	Name      string              `json:"name"`
	Icon      string              `json:"icon"`
	Color     string              `json:"color"`
	IsHidden  bool                `json:"is_hidden"`
	CreatedAt helpers.EpochMillis `json:"created_at"`
	UpdatedAt helpers.EpochMillis `json:"updated_at"`
}

type adminCreatePredefinedRequest struct {
	Key   string `json:"key"   validate:"required,min=1,max=100"`
	Name  string `json:"name"  validate:"required,min=1,max=100"`
	Icon  string `json:"icon"  validate:"required,min=1,max=100"`
	Color string `json:"color" validate:"required,len=7"`
}

type adminUpdatePredefinedRequest struct {
	Name     *string `json:"name,omitempty"  validate:"omitempty,min=1,max=100"`
	Icon     *string `json:"icon,omitempty"  validate:"omitempty,min=1,max=100"`
	Color    *string `json:"color,omitempty" validate:"omitempty,len=7"`
	IsHidden *bool   `json:"is_hidden,omitempty"`
}

// AdminListPredefined returns all predefined categories (including hidden
// rows). Admin only.
func AdminListPredefined(c *gin.Context, d *db.DB) {
	rows, err := d.Pool.Query(c.Request.Context(),
		`SELECT id, key, name, icon, color, is_hidden, created_at, updated_at
		   FROM predefined_categories
		  ORDER BY name ASC`)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve predefined categories"})
		return
	}
	defer rows.Close()

	out := make([]AdminPredefinedCategory, 0)
	for rows.Next() {
		var p AdminPredefinedCategory
		var rawCreatedAt, rawUpdatedAt time.Time
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Icon, &p.Color,
			&p.IsHidden, &rawCreatedAt, &rawUpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan predefined category"})
			return
		}
		p.CreatedAt = helpers.FromTime(rawCreatedAt)
		p.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}
	c.JSON(200, gin.H{"data": out})
}

// AdminCreatePredefined inserts a new predefined category. The key must be
// unique. The icon must reference one of the embedded SVG icons.
func AdminCreatePredefined(c *gin.Context, d *db.DB) {
	var req adminCreatePredefinedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if !IsValidIconKey(req.Icon) {
		c.JSON(400, gin.H{"error": "invalid icon key", "code": "INVALID_ICON"})
		return
	}
	if !hexColorRegex.MatchString(req.Color) {
		c.JSON(400, gin.H{"error": "color must be hex #RRGGBB", "code": "INVALID_COLOR"})
		return
	}

	var p AdminPredefinedCategory
	var rawCreatedAt, rawUpdatedAt time.Time
	err := d.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO predefined_categories (key, name, icon, color)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, key, name, icon, color, is_hidden, created_at, updated_at`,
		req.Key, req.Name, req.Icon, req.Color).Scan(
		&p.ID, &p.Key, &p.Name, &p.Icon, &p.Color,
		&p.IsHidden, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(409, gin.H{"error": "predefined category key already exists", "code": "KEY_EXISTS"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to create predefined category"})
		return
	}
	p.CreatedAt = helpers.FromTime(rawCreatedAt)
	p.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	c.JSON(201, p)
}

// AdminUpdatePredefined edits a predefined category. The protected key
// (ProtectedKey, "other") cannot be hidden.
func AdminUpdatePredefined(c *gin.Context, d *db.DB) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	var req adminUpdatePredefinedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Name == nil && req.Icon == nil && req.Color == nil && req.IsHidden == nil {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}
	if req.Icon != nil && !IsValidIconKey(*req.Icon) {
		c.JSON(400, gin.H{"error": "invalid icon key", "code": "INVALID_ICON"})
		return
	}
	if req.Color != nil && !hexColorRegex.MatchString(*req.Color) {
		c.JSON(400, gin.H{"error": "color must be hex #RRGGBB", "code": "INVALID_COLOR"})
		return
	}

	// Look up the existing row to enforce the protected-key constraint.
	var existingKey string
	if err := d.Pool.QueryRow(c.Request.Context(),
		`SELECT key FROM predefined_categories WHERE id = $1`, id).Scan(&existingKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(404, gin.H{"error": "predefined category not found"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to lookup predefined category"})
		return
	}
	if existingKey == ProtectedKey && req.IsHidden != nil && *req.IsHidden {
		c.JSON(400, gin.H{
			"error": fmt.Sprintf("the protected category %q cannot be hidden", ProtectedKey),
			"code":  "PROTECTED_CATEGORY",
		})
		return
	}

	query := `UPDATE predefined_categories SET `
	args := []interface{}{}
	argCount := 1
	if req.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argCount)
		args = append(args, *req.Name)
		argCount++
	}
	if req.Icon != nil {
		query += fmt.Sprintf("icon = $%d, ", argCount)
		args = append(args, *req.Icon)
		argCount++
	}
	if req.Color != nil {
		query += fmt.Sprintf("color = $%d, ", argCount)
		args = append(args, *req.Color)
		argCount++
	}
	if req.IsHidden != nil {
		query += fmt.Sprintf("is_hidden = $%d, ", argCount)
		args = append(args, *req.IsHidden)
		argCount++
	}
	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d RETURNING id, key, name, icon, color, is_hidden, created_at, updated_at", argCount)
	args = append(args, id)

	var p AdminPredefinedCategory
	var rawCreatedAt, rawUpdatedAt time.Time
	err = d.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&p.ID, &p.Key, &p.Name, &p.Icon, &p.Color,
		&p.IsHidden, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update predefined category"})
		return
	}
	p.CreatedAt = helpers.FromTime(rawCreatedAt)
	p.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	c.JSON(200, p)
}

// AdminDeletePredefined supports two modes:
//   - default → soft delete (set is_hidden = true)
//   - ?hard=true → permanent delete; user override rows for the same key are
//     also removed (cascade in Go, no DB FK).
//
// The protected key ("other") cannot be deleted by either mode.
func AdminDeletePredefined(c *gin.Context, d *db.DB) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}
	hard := c.Query("hard") == "true"

	var key string
	if err := d.Pool.QueryRow(c.Request.Context(),
		`SELECT key FROM predefined_categories WHERE id = $1`, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(404, gin.H{"error": "predefined category not found"})
			return
		}
		c.JSON(500, gin.H{"error": "failed to lookup predefined category"})
		return
	}
	if key == ProtectedKey {
		c.JSON(400, gin.H{
			"error": fmt.Sprintf("the protected category %q cannot be deleted", ProtectedKey),
			"code":  "PROTECTED_CATEGORY",
		})
		return
	}

	if !hard {
		if _, err := d.Pool.Exec(c.Request.Context(),
			`UPDATE predefined_categories SET is_hidden = TRUE, updated_at = NOW() WHERE id = $1`,
			id); err != nil {
			c.JSON(500, gin.H{"error": "failed to soft-delete predefined category"})
			return
		}
		c.JSON(200, gin.H{"message": "predefined category hidden"})
		return
	}

	// Hard delete: cascade-remove user override rows that reference this key,
	// then drop the predefined row itself. Wrap in a transaction so a failure
	// part-way through doesn't leave orphans.
	tx, err := d.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to begin transaction"})
		return
	}
	defer tx.Rollback(c.Request.Context()) //nolint:errcheck

	// Cascade all transactions that referenced this predefined key to "other".
	for _, q := range []string{
		`UPDATE transactions SET category = 'other' WHERE category = $1`,
		`UPDATE group_transactions SET category = 'other' WHERE category = $1`,
		`UPDATE recurring_transactions SET category = 'other' WHERE category = $1`,
	} {
		if _, err := tx.Exec(c.Request.Context(), q, key); err != nil {
			c.JSON(500, gin.H{"error": "failed to reassign transactions"})
			return
		}
	}
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM custom_categories WHERE is_predefined = TRUE AND predefined_key = $1`,
		key); err != nil {
		c.JSON(500, gin.H{"error": "failed to delete user overrides"})
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM predefined_categories WHERE id = $1`, id); err != nil {
		c.JSON(500, gin.H{"error": "failed to hard-delete predefined category"})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(500, gin.H{"error": "failed to commit transaction"})
		return
	}
	c.JSON(200, gin.H{"message": "predefined category permanently deleted"})
}
