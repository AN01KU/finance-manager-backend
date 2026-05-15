package category

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

// ProtectedKey is the predefined category key that can never be hidden or
// deleted, even by an admin. It is the catch-all "Other" bucket.
const ProtectedKey = "other"

var validate = validator.New()

// hexColorRegex enforces a #RRGGBB format (uppercase or lowercase hex).
var hexColorRegex = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

//go:embed icons/*.svg
var iconsFS embed.FS

// validIconKeys is the set of all valid kebab-case icon keys derived from the
// embedded SVG file list at startup.
var validIconKeys = func() map[string]struct{} {
	entries, err := iconsFS.ReadDir("icons")
	if err != nil {
		panic(fmt.Sprintf("category: failed to read embedded icons dir: %v", err))
	}
	m := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".svg") {
			continue
		}
		m[strings.TrimSuffix(name, ".svg")] = struct{}{}
	}
	return m
}()

// IsValidIconKey reports whether the given kebab-case key matches one of the
// embedded SVG icons.
func IsValidIconKey(key string) bool {
	_, ok := validIconKeys[key]
	return ok
}

// IsValidHexColor reports whether s is a valid #RRGGBB hex colour string.
func IsValidHexColor(s string) bool {
	return hexColorRegex.MatchString(s)
}

// ValidIconKeys returns a sorted slice of every valid icon key. Useful for
// rendering admin <select> pickers.
func ValidIconKeys() []string {
	out := make([]string, 0, len(validIconKeys))
	for k := range validIconKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IconSVG returns the raw SVG bytes for the given key. Returns ok=false if
// the key does not match any embedded icon. Used by the admin panel to render
// inline icon previews.
func IconSVG(key string) ([]byte, bool) {
	if !IsValidIconKey(key) {
		return nil, false
	}
	data, err := iconsFS.ReadFile("icons/" + key + ".svg")
	if err != nil {
		return nil, false
	}
	return data, true
}

// Category is the merged shape returned by GET /categories. It represents
// either a predefined category (with optional user override applied), or a
// user's custom category.
type Category struct {
	ID            uuid.UUID           `json:"id"`
	Key           string              `json:"key"`
	UserID        uuid.UUID           `json:"user_id"`
	Name          string              `json:"name,omitempty"`
	Icon          string              `json:"icon,omitempty"`
	Color         string              `json:"color,omitempty"`
	IsHidden      bool                `json:"is_hidden"`
	IsPredefined  bool                `json:"is_predefined"`
	PredefinedKey *string             `json:"predefined_key,omitempty"`
	IsDeleted     bool                `json:"is_deleted,omitempty"`
	CreatedAt     helpers.EpochMillis `json:"created_at,omitempty"`
	UpdatedAt     helpers.EpochMillis `json:"updated_at,omitempty"`
}

// PredefinedCategory is the public shape returned by GET /predefined-categories.
type PredefinedCategory struct {
	ID    uuid.UUID `json:"id"`
	Key   string    `json:"key"`
	Name  string    `json:"name"`
	Icon  string    `json:"icon"`
	Color string    `json:"color"`
}

// CreateCategoryRequest is the body for POST /categories.
//
//   - Without predefined_key: creates a new custom category. name/icon/color
//     are all required.
//   - With predefined_key: creates an override row for the given predefined
//     category. Any of name/icon/color may be omitted to inherit the
//     predefined defaults. Fails with 409 if an override already exists, or
//     404 if the key is unknown or admin-hidden.
//
// is_hidden is optional in both modes and defaults to false. Setting it true
// when creating a predefined override is the supported way to hide a
// predefined category for this user.
type CreateCategoryRequest struct {
	Name          *string `json:"name,omitempty"           validate:"omitempty,min=1,max=100"`
	Icon          *string `json:"icon,omitempty"           validate:"omitempty,min=1,max=100"`
	Color         *string `json:"color,omitempty"          validate:"omitempty,len=7"`
	PredefinedKey *string `json:"predefined_key,omitempty" validate:"omitempty,min=1,max=50"`
	IsHidden      *bool   `json:"is_hidden,omitempty"`
}

type UpdateCategoryRequest struct {
	Name     *string `json:"name,omitempty"      validate:"omitempty,min=1,max=100"`
	Icon     *string `json:"icon,omitempty"      validate:"omitempty,min=1,max=100"`
	Color    *string `json:"color,omitempty"     validate:"omitempty,len=7"`
	IsHidden *bool   `json:"is_hidden,omitempty"`
}

// predefinedRow is the internal lookup shape for a row in the
// predefined_categories table.
type predefinedRow struct {
	Key   string
	Name  string
	Icon  string
	Color string
}

// loadVisiblePredefined returns all non-hidden predefined categories ordered
// by name. Used by the user-facing list endpoint.
func loadVisiblePredefined(ctx context.Context, d *db.DB) ([]predefinedRow, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT key, name, icon, color
		   FROM predefined_categories
		  WHERE is_hidden = FALSE
		  ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []predefinedRow
	for rows.Next() {
		var p predefinedRow
		if err := rows.Scan(&p.Key, &p.Name, &p.Icon, &p.Color); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// virtualPredefinedID returns the deterministic UUID used to identify a
// predefined category that has no override row for the given user.
func virtualPredefinedID(userID uuid.UUID, key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(userID.String()+key))
}

// GetPredefinedCategoriesHandler exposes the master predefined list. Open
// endpoint, no auth required. Hidden rows are excluded.
func GetPredefinedCategoriesHandler(c *gin.Context, d *db.DB) {
	rows, err := d.Pool.Query(c.Request.Context(),
		`SELECT id, key, name, icon, color
		   FROM predefined_categories
		  WHERE is_hidden = FALSE
		  ORDER BY name ASC`)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve predefined categories"})
		return
	}
	defer rows.Close()

	out := make([]PredefinedCategory, 0)
	for rows.Next() {
		var p PredefinedCategory
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Icon, &p.Color); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan predefined category"})
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}
	c.JSON(200, gin.H{"data": out})
}

// CreateCategory creates either a custom user category or a predefined
// override row (when predefined_key is supplied). See CreateCategoryRequest
// for the exact field semantics of each mode.
func CreateCategory(c *gin.Context, d *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
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

	if req.PredefinedKey != nil {
		createPredefinedOverride(c, d, userID, req)
		return
	}

	// Custom category: name/icon/color are all required.
	if req.Name == nil || req.Icon == nil || req.Color == nil {
		c.JSON(400, gin.H{"error": "name, icon and color are required for custom categories"})
		return
	}

	catKey := "cc-" + uuid.New().String()
	isHidden := false
	if req.IsHidden != nil {
		isHidden = *req.IsHidden
	}

	cat := Category{}
	var rawCreatedAt, rawUpdatedAt time.Time
	err := d.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key, key)
		 VALUES ($1, $2, $3, $4, $5, FALSE, NULL, $6)
		 RETURNING id, key, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
		userID, *req.Name, *req.Icon, *req.Color, isHidden, catKey).Scan(
		&cat.ID, &cat.Key, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
		&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create category"})
		return
	}
	cat.CreatedAt = helpers.FromTime(rawCreatedAt)
	cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	c.JSON(201, cat)
}

// createPredefinedOverride inserts a custom_categories row that overrides the
// named predefined category for this user. Omitted fields fall back to the
// predefined defaults. Returns:
//   - 404 if the predefined key is unknown or admin-hidden.
//   - 409 if the user already has an override row for that key (caller
//     should switch to PATCH /categories/:id).
func createPredefinedOverride(c *gin.Context, d *db.DB, userID uuid.UUID, req CreateCategoryRequest) {
	key := *req.PredefinedKey

	// The "other" category is the catch-all bucket and cannot be hidden.
	if key == ProtectedKey && req.IsHidden != nil && *req.IsHidden {
		c.JSON(400, gin.H{
			"error": fmt.Sprintf("the %q category cannot be hidden", ProtectedKey),
			"code":  "PROTECTED_CATEGORY",
		})
		return
	}

	// Look up the predefined defaults. is_hidden = TRUE means the admin has
	// retired the category from user-facing surfaces — reject the override.
	var def predefinedRow
	err := d.Pool.QueryRow(c.Request.Context(),
		`SELECT key, name, icon, color
		   FROM predefined_categories
		  WHERE key = $1
		    AND is_hidden = FALSE`, key).Scan(&def.Key, &def.Name, &def.Icon, &def.Color)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(404, gin.H{
			"error": fmt.Sprintf("predefined category %q does not exist or is no longer available", key),
			"code":  "PREDEFINED_NOT_FOUND",
		})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to load predefined category"})
		return
	}

	name := def.Name
	icon := def.Icon
	color := def.Color
	isHidden := false
	if req.Name != nil {
		name = *req.Name
	}
	if req.Icon != nil {
		icon = *req.Icon
	}
	if req.Color != nil {
		color = *req.Color
	}
	if req.IsHidden != nil {
		isHidden = *req.IsHidden
	}

	overrideID := virtualPredefinedID(userID, def.Key)
	overrideKey := "oc-" + def.Key

	cat := Category{}
	var rawCreatedAt, rawUpdatedAt time.Time
	err = d.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO custom_categories (id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, key)
		 VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7, $8)
		 RETURNING id, key, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
		overrideID, userID, name, icon, color, isHidden, def.Key, overrideKey).Scan(
		&cat.ID, &cat.Key, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
		&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(409, gin.H{
				"error": fmt.Sprintf("an override for predefined category %q already exists; use PATCH /categories/:id to update it", def.Key),
				"code":  "OVERRIDE_ALREADY_EXISTS",
			})
			return
		}
		c.JSON(500, gin.H{"error": "failed to create category override"})
		return
	}
	// Clients reference this row by the predefined key, not the synthetic override key.
	cat.Key = def.Key
	cat.CreatedAt = helpers.FromTime(rawCreatedAt)
	cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	c.JSON(201, cat)
}

// ListCategories returns the authenticated user's own custom_categories rows
// (including override rows for predefined categories). Clients fetch the
// predefined catalogue separately via GET /predefined-categories.
func ListCategories(c *gin.Context, d *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	rows, err := d.Pool.Query(c.Request.Context(),
		`SELECT id, key, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, is_deleted, created_at, updated_at
		   FROM custom_categories
		  WHERE user_id = $1
		    AND is_deleted = FALSE
		  ORDER BY created_at ASC`,
		userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve categories"})
		return
	}
	defer rows.Close()

	out := make([]Category, 0)
	for rows.Next() {
		var cat Category
		var rawCreatedAt, rawUpdatedAt time.Time
		if err := rows.Scan(&cat.ID, &cat.Key, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
			&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &cat.IsDeleted, &rawCreatedAt, &rawUpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan category"})
			return
		}
		cat.CreatedAt = helpers.FromTime(rawCreatedAt)
		cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		// Override rows: expose the predefined key so the client can match it back.
		if cat.IsPredefined && cat.PredefinedKey != nil {
			cat.Key = *cat.PredefinedKey
		}
		out = append(out, cat)
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
	}

	c.JSON(200, gin.H{"data": out})
}

// UpdateCategory updates an existing category. If the id matches a virtual
// predefined UUID for the caller (no DB row yet), an override row is created
// from the predefined defaults plus the requested changes.
func UpdateCategory(c *gin.Context, d *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	categoryIDStr := c.Param("id")
	categoryID, err := uuid.Parse(categoryIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid category id"})
		return
	}

	var req UpdateCategoryRequest
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

	// Try to find an existing row first.
	var ownerID uuid.UUID
	err = d.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM custom_categories WHERE id = $1`, categoryID).Scan(&ownerID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		c.JSON(500, gin.H{"error": "failed to lookup category"})
		return
	}

	if errors.Is(err, pgx.ErrNoRows) {
		// No DB row — see if this matches a virtual predefined UUID for the
		// caller. We need to know which predefined key it corresponds to.
		predefined, err := loadVisiblePredefined(c.Request.Context(), d)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to load predefined categories"})
			return
		}
		var matched *predefinedRow
		for i := range predefined {
			if virtualPredefinedID(userID, predefined[i].Key) == categoryID {
				matched = &predefined[i]
				break
			}
		}
		if matched == nil {
			c.JSON(404, gin.H{"error": "category not found"})
			return
		}
		if matched.Key == ProtectedKey && req.IsHidden != nil && *req.IsHidden {
			c.JSON(400, gin.H{
				"error": fmt.Sprintf("the %q category cannot be hidden", ProtectedKey),
				"code":  "PROTECTED_CATEGORY",
			})
			return
		}

		name := matched.Name
		icon := matched.Icon
		color := matched.Color
		isHidden := false
		if req.Name != nil {
			name = *req.Name
		}
		if req.Icon != nil {
			icon = *req.Icon
		}
		if req.Color != nil {
			color = *req.Color
		}
		if req.IsHidden != nil {
			isHidden = *req.IsHidden
		}

		overrideKey := "oc-" + matched.Key
		var cat Category
		var rawCreatedAt, rawUpdatedAt time.Time
		err = d.Pool.QueryRow(c.Request.Context(),
			`INSERT INTO custom_categories (id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, key)
			 VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7, $8)
			 RETURNING id, key, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
			categoryID, userID, name, icon, color, isHidden, matched.Key, overrideKey).Scan(
			&cat.ID, &cat.Key, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
			&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create category override"})
			return
		}
		// Clients reference this via the predefined key, not the synthetic override key.
		cat.Key = matched.Key
		cat.CreatedAt = helpers.FromTime(rawCreatedAt)
		cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		c.JSON(200, cat)
		return
	}

	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this category"})
		return
	}

	// Guard: the "other" predefined category cannot be hidden via an existing override row.
	if req.IsHidden != nil && *req.IsHidden {
		var predKey *string
		_ = d.Pool.QueryRow(c.Request.Context(),
			`SELECT predefined_key FROM custom_categories WHERE id = $1`, categoryID).Scan(&predKey)
		if predKey != nil && *predKey == ProtectedKey {
			c.JSON(400, gin.H{
				"error": fmt.Sprintf("the %q category cannot be hidden", ProtectedKey),
				"code":  "PROTECTED_CATEGORY",
			})
			return
		}
	}

	// Existing DB row — build dynamic update.
	// Always clear is_deleted so that PATCHing a soft-deleted predefined override
	// resurrects it rather than silently updating a row that the list won't return.
	query := `UPDATE custom_categories SET is_deleted = FALSE, `
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
	query += fmt.Sprintf("WHERE id = $%d RETURNING id, key, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at", argCount)
	args = append(args, categoryID)

	var cat Category
	var rawCreatedAt, rawUpdatedAt time.Time
	err = d.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&cat.ID, &cat.Key, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
		&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update category"})
		return
	}
	// Override rows: expose the predefined key to clients.
	if cat.IsPredefined && cat.PredefinedKey != nil {
		cat.Key = *cat.PredefinedKey
	}
	cat.CreatedAt = helpers.FromTime(rawCreatedAt)
	cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)
	c.JSON(200, cat)
}

// DeleteCategory deletes a user category row.
//   - Predefined override row → row is removed; the user reverts to the
//     predefined default on the next list call.
//   - Custom category row → permanent delete.
//   - Virtual predefined UUIDs (no DB row) → 404; nothing to delete.
//   - The protected predefined ("other") cannot be deleted as an override
//     either — its override row is allowed to be deleted (resetting to
//     defaults), but the underlying predefined cannot disappear.
func DeleteCategory(c *gin.Context, d *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	categoryIDStr := c.Param("id")
	categoryID, err := uuid.Parse(categoryIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid category id"})
		return
	}

	var ownerID uuid.UUID
	var predefinedKey *string
	var customKey string
	err = d.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, predefined_key, key FROM custom_categories WHERE id = $1`, categoryID).
		Scan(&ownerID, &predefinedKey, &customKey)
	if err != nil {
		c.JSON(404, gin.H{"error": "category not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this category"})
		return
	}

	// For custom (non-predefined) categories, cascade transactions to "other"
	// before soft-deleting so no transaction references a dangling key.
	if predefinedKey == nil {
		tx, err := d.Pool.Begin(c.Request.Context())
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to begin transaction"})
			return
		}
		defer tx.Rollback(c.Request.Context()) //nolint:errcheck

		for _, q := range []string{
			`UPDATE transactions SET category = 'other' WHERE category = $1 AND user_id = $2`,
			`UPDATE recurring_transactions SET category = 'other' WHERE category = $1 AND user_id = $2`,
		} {
			if _, err := tx.Exec(c.Request.Context(), q, customKey, userID); err != nil {
				c.JSON(500, gin.H{"error": "failed to reassign transactions"})
				return
			}
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE group_transactions SET category = 'other' WHERE category = $1 AND paid_by_user_id = $2`,
			customKey, userID); err != nil {
			c.JSON(500, gin.H{"error": "failed to reassign group transactions"})
			return
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE custom_categories SET is_deleted = TRUE, updated_at = NOW() WHERE id = $1`, categoryID); err != nil {
			c.JSON(500, gin.H{"error": "failed to delete category"})
			return
		}
		if err := tx.Commit(c.Request.Context()); err != nil {
			c.JSON(500, gin.H{"error": "failed to commit"})
			return
		}
		c.JSON(200, gin.H{"message": "category deleted successfully"})
		return
	}

	// Predefined override row — soft-delete so the client gets a tombstone and
	// knows to revert to the predefined default.
	if _, err := d.Pool.Exec(c.Request.Context(),
		`UPDATE custom_categories SET is_deleted = TRUE, updated_at = NOW() WHERE id = $1`, categoryID); err != nil {
		c.JSON(500, gin.H{"error": "failed to reset category"})
		return
	}
	c.JSON(200, gin.H{"message": "category reset to default"})
}
