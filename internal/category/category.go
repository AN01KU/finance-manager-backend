package category

import (
	"context"
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
	ID    *uuid.UUID `json:"id,omitempty"`
	Name  string     `json:"name" validate:"required,min=1,max=100"`
	Icon  string     `json:"icon" validate:"required,max=50"`
	Color string     `json:"color" validate:"required,len=7"`
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

// SeedPredefinedCategories inserts the predefined categories for a user.
func SeedPredefinedCategories(ctx context.Context, database *db.DB, userID uuid.UUID) error {
	query := `INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key) VALUES `
	args := []interface{}{}
	argCount := 1

	for i, cat := range predefinedCategories {
		if i > 0 {
			query += ", "
		}
		query += fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			argCount, argCount+1, argCount+2, argCount+3, argCount+4, argCount+5, argCount+6)
		args = append(args, userID, cat.Name, cat.Icon, cat.Color, false, true, cat.Key)
		argCount += 7
	}

	query += ` ON CONFLICT (user_id, predefined_key) WHERE predefined_key IS NOT NULL DO NOTHING`

	_, err := database.Pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to seed predefined categories: %w", err)
	}
	return nil
}

// CreateCategory creates a new custom category
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

	var category CustomCategory
	var rawCreatedAt, rawUpdatedAt time.Time
	err := db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO custom_categories (id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
		 VALUES ($1, $2, $3, $4, $5, false, false, NULL)
		 ON CONFLICT (id) DO UPDATE SET
		   name = EXCLUDED.name,
		   icon = EXCLUDED.icon,
		   color = EXCLUDED.color,
		   updated_at = NOW()
		 RETURNING id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at`,
		categoryID, userID, req.Name, req.Icon, req.Color).Scan(
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

// ListCategories retrieves all categories for a user
func ListCategories(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT id, user_id, name, icon, color, is_hidden, is_predefined, predefined_key, created_at, updated_at
		 FROM custom_categories
		 WHERE user_id = $1
		 ORDER BY is_predefined DESC, name ASC`,
		userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve categories"})
		return
	}
	defer rows.Close()

	var categories []CustomCategory
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
		categories = append(categories, cat)
	}

	if categories == nil {
		categories = []CustomCategory{}
	}

	c.JSON(200, gin.H{"data": categories})
}

// UpdateCategory updates an existing category
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

	// Check if category belongs to user
	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM custom_categories WHERE id = $1`, categoryID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "category not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this category"})
		return
	}

	// Build update query dynamically
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

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
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

// DeleteCategory deletes a category
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

	// Check if category belongs to user and get predefined_key
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

	c.JSON(200, gin.H{"message": "category deleted successfully"})
}
