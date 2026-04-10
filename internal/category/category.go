package category

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type CustomCategory struct {
	ID            uuid.UUID           `json:"id"`
	UserID        uuid.UUID           `json:"user_id"`
	Name          string              `json:"name"`
	Icon          string              `json:"icon"`
	Color         string              `json:"color"`
	IsHidden      bool                `json:"is_hidden"`
	IsPredefined  bool                `json:"is_predefined"`
	PredefinedKey *string             `json:"predefined_key,omitempty"`
	CreatedAt     helpers.EpochMillis `json:"created_at"`
	UpdatedAt     helpers.EpochMillis `json:"updated_at"`
}

type CreateCategoryRequest struct {
	ID            *uuid.UUID `json:"id,omitempty"`
	Name          string     `json:"name" validate:"required,min=1,max=100"`
	Icon          string     `json:"icon" validate:"required,max=50"`
	Color         string     `json:"color" validate:"required,len=7"`
	PredefinedKey *string    `json:"predefined_key,omitempty"`
	IsHidden      *bool      `json:"is_hidden,omitempty"`
}

type UpdateCategoryRequest struct {
	Name     *string `json:"name,omitempty" validate:"omitempty,min=1,max=100"`
	Icon     *string `json:"icon,omitempty" validate:"omitempty,max=50"`
	Color    *string `json:"color,omitempty" validate:"omitempty,len=7"`
	IsHidden *bool   `json:"is_hidden,omitempty"`
}

type predefinedCategory struct {
	Key   string
	Name  string
	Icon  string
	Color string
}

var predefinedCategories = []predefinedCategory{
	{"foodDining", "Food & Dining", "fork.knife.circle.fill", "#FF6B6B"},
	{"transport", "Transport", "car.circle.fill", "#4ECDC4"},
	{"housing", "Housing", "house.circle.fill", "#45B7D1"},
	{"healthMedical", "Health & Medical", "cross.case.circle.fill", "#96CEB4"},
	{"shopping", "Shopping", "bag.circle.fill", "#FFEAA7"},
	{"utilities", "Utilities", "bolt.square.fill", "#DDA15E"},
	{"entertainment", "Entertainment", "gamecontroller.circle.fill", "#BC6C25"},
	{"travel", "Travel", "airplane.circle.fill", "#8E44AD"},
	{"workProfessional", "Work & Professional", "briefcase.circle.fill", "#34495E"},
	{"education", "Education", "book.circle.fill", "#3498DB"},
	{"debtPayments", "Debt & Payments", "creditcard.circle.fill", "#2C3E50"},
	{"booksMedia", "Books & Media", "book.closed.circle.fill", "#E74C3C"},
	{"familyKids", "Family & Kids", "figure.2.and.child.holdinghands", "#F39C12"},
	{"gifts", "Gifts", "gift.circle.fill", "#E91E63"},
	{"other", "Other", "ellipsis.circle.fill", "#95A5A6"},
}

// predefinedByKey builds a lookup map for quick access.
var predefinedByKey = func() map[string]predefinedCategory {
	m := make(map[string]predefinedCategory, len(predefinedCategories))
	for _, c := range predefinedCategories {
		m[c.Key] = c
	}
	return m
}()

// GetPredefinedCategories returns the static predefined list (used by API endpoints like /predefined-categories).
func GetPredefinedCategories() []predefinedCategory {
	return predefinedCategories
}

// CreateCategory creates or upserts a category (custom or predefined override).
func CreateCategory(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	categoryID := uuid.New()
	if req.ID != nil {
		categoryID = *req.ID
	}

	isPredefined := req.PredefinedKey != nil

	var category CustomCategory
	var rawCreatedAt, rawUpdatedAt time.Time
	err := db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO custom_categories (id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
		 VALUES ($1, $2, $3, $4, $5, COALESCE($6, false), $7, $8)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   icon = EXCLUDED.icon,
		   color = EXCLUDED.color,
		   is_hidden = EXCLUDED.is_hidden,
		   updated_at = NOW()
		 RETURNING id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
		categoryID, userID, req.Name, req.Icon, req.Color, req.IsHidden, isPredefined, req.PredefinedKey).Scan(
		&category.ID, &category.UserID, &category.Name, &category.Icon, &category.Color,
		&category.IsHidden, &category.IsPredefined, &category.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create category"})
		return
	}
	category.CreatedAt = helpers.FromTime(rawCreatedAt)
	category.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(201, category)
}

// ListCategories returns a merged list: predefined defaults (with user overrides applied) + user's custom categories.
// DB rows only exist for overrides and custom categories — untouched predefined categories are generated in-memory.
func ListCategories(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	// Fetch only the user's rows (overrides + custom).
	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at
		 FROM custom_categories
		 WHERE user_id = $1`,
		userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve categories"})
		return
	}
	defer rows.Close()

	// Build a map of predefined_key → override row.
	overrides := make(map[string]CustomCategory)
	var customCats []CustomCategory
	for rows.Next() {
		var cat CustomCategory
		var rawCreatedAt, rawUpdatedAt time.Time
		if err := rows.Scan(&cat.ID, &cat.UserID, &cat.Name, &cat.Icon, &cat.Color,
			&cat.IsHidden, &cat.IsPredefined, &cat.PredefinedKey, &rawCreatedAt, &rawUpdatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan category"})
			return
		}
		cat.CreatedAt = helpers.FromTime(rawCreatedAt)
		cat.UpdatedAt = helpers.FromTime(rawUpdatedAt)

		if cat.IsPredefined && cat.PredefinedKey != nil {
			overrides[*cat.PredefinedKey] = cat
		} else {
			customCats = append(customCats, cat)
		}
	}

	// Build the merged list: predefined first (with overrides applied), then custom.
	now := helpers.FromTime(time.Now())
	categories := make([]CustomCategory, 0, len(predefinedCategories)+len(customCats))

	for _, p := range predefinedCategories {
		if override, ok := overrides[p.Key]; ok {
			// User has an override row for this predefined category.
			categories = append(categories, override)
		} else {
			// No override — return the static default (no DB row exists).
			key := p.Key
			categories = append(categories, CustomCategory{
				ID:            uuid.NewSHA1(uuid.NameSpaceDNS, []byte(userID.String()+p.Key)),
				UserID:        userID,
				Name:          p.Name,
				Icon:          p.Icon,
				Color:         p.Color,
				IsHidden:      false,
				IsPredefined:  true,
				PredefinedKey: &key,
				CreatedAt:     now,
				UpdatedAt:     now,
			})
		}
	}

	categories = append(categories, customCats...)

	c.JSON(200, gin.H{"data": categories})
}

// UpdateCategory updates an existing category.
// If the category ID matches a virtual predefined (no DB row yet), it creates an override row.
func UpdateCategory(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
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

	validate := validator.New()
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Name == nil && req.Icon == nil && req.Color == nil && req.IsHidden == nil {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	// Check if this is an existing DB row.
	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM custom_categories WHERE id = $1`, categoryID).Scan(&ownerID)

	if err != nil {
		// No DB row — check if this is a virtual predefined category ID.
		var matchedKey string
		for _, p := range predefinedCategories {
			virtualID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(userID.String()+p.Key))
			if virtualID == categoryID {
				matchedKey = p.Key
				break
			}
		}
		if matchedKey == "" {
			c.JSON(404, gin.H{"error": "category not found"})
			return
		}

		// Create an override row from the predefined default + the requested changes.
		p := predefinedByKey[matchedKey]
		name := p.Name
		icon := p.Icon
		color := p.Color
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

		var category CustomCategory
		var rawCreatedAt, rawUpdatedAt time.Time
		err = db.Pool.QueryRow(c.Request.Context(),
			`INSERT INTO custom_categories (id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
			 VALUES ($1, $2, $3, $4, $5, $6, true, $7)
			 RETURNING id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
			categoryID, userID, name, icon, color, isHidden, matchedKey).Scan(
			&category.ID, &category.UserID, &category.Name, &category.Icon, &category.Color,
			&category.IsHidden, &category.IsPredefined, &category.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create category override"})
			return
		}
		category.CreatedAt = helpers.FromTime(rawCreatedAt)
		category.UpdatedAt = helpers.FromTime(rawUpdatedAt)
		c.JSON(200, category)
		return
	}

	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this category"})
		return
	}

	// Existing DB row — build dynamic update.
	query := `UPDATE custom_categories SET `
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

	query += fmt.Sprintf("updated_at = NOW() ")
	query += fmt.Sprintf("WHERE id = $%d RETURNING id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at", argCount)
	args = append(args, categoryID)

	var category CustomCategory
	var rawCreatedAt, rawUpdatedAt time.Time
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&category.ID, &category.UserID, &category.Name, &category.Icon, &category.Color,
		&category.IsHidden, &category.IsPredefined, &category.PredefinedKey, &rawCreatedAt, &rawUpdatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update category"})
		return
	}
	category.CreatedAt = helpers.FromTime(rawCreatedAt)
	category.UpdatedAt = helpers.FromTime(rawUpdatedAt)

	c.JSON(200, category)
}

// DeleteCategory deletes a category.
// For predefined overrides: deletes the override row (user gets back the default).
// For custom categories: permanent delete.
// The "Other" predefined can never be deleted.
func DeleteCategory(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	categoryIDStr := c.Param("id")
	categoryID, err := uuid.Parse(categoryIDStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid category id"})
		return
	}

	// Check if category belongs to user and get predefined_key.
	var ownerID uuid.UUID
	var predefinedKey *string
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id, predefined_key FROM custom_categories WHERE id = $1`, categoryID).Scan(&ownerID, &predefinedKey)
	if err != nil {
		c.JSON(404, gin.H{"error": "category not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this category"})
		return
	}

	if predefinedKey != nil && *predefinedKey == "other" {
		c.JSON(400, gin.H{"error": "cannot delete the 'Other' category"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM custom_categories WHERE id = $1`, categoryID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete category"})
		return
	}

	if predefinedKey != nil {
		c.JSON(200, gin.H{"message": "category reset to default"})
	} else {
		c.JSON(200, gin.H{"message": "category deleted successfully"})
	}
}
