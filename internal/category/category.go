package category

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type ExpenseCategory struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Name      string    `json:"name" db:"name"`
	Color     string    `json:"color" db:"color"`
	Icon      string    `json:"icon" db:"icon"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type CreateCategoryRequest struct {
	Name  string `json:"name" validate:"required,min=1,max=100"`
	Color string `json:"color" validate:"omitempty,len=7"`
	Icon  string `json:"icon" validate:"omitempty,max=50"`
}

type UpdateCategoryRequest struct {
	Name  *string `json:"name,omitempty" validate:"omitempty,min=1,max=100"`
	Color *string `json:"color,omitempty" validate:"omitempty,len=7"`
	Icon  *string `json:"icon,omitempty" validate:"omitempty,max=50"`
}

// CreateCategory creates a new expense category
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

	var category ExpenseCategory
	color := req.Color
	icon := req.Icon
	if color == "" {
		color = "#000000"
	}
	if icon == "" {
		icon = "category"
	}
	err := db.Pool.QueryRow(c.Request.Context(),
		`INSERT INTO expense_categories (user_id, name, color, icon) 
		 VALUES ($1, $2, $3, $4) 
		 RETURNING id, user_id, name, COALESCE(color, ''), COALESCE(icon, ''), created_at`,
		userID, req.Name, color, icon).Scan(
		&category.ID, &category.UserID, &category.Name, &category.Color, &category.Icon, &category.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create category"})
		return
	}

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
		`SELECT id, user_id, name, COALESCE(color, ''), COALESCE(icon, ''), created_at 
		 FROM expense_categories 
		 WHERE user_id = $1 
		 ORDER BY name ASC`,
		userID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve categories"})
		return
	}
	defer rows.Close()

	var categories []ExpenseCategory
	for rows.Next() {
		var cat ExpenseCategory
		if err := rows.Scan(&cat.ID, &cat.UserID, &cat.Name, &cat.Color, &cat.Icon, &cat.CreatedAt); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan category"})
			return
		}
		categories = append(categories, cat)
	}

	if categories == nil {
		categories = []ExpenseCategory{}
	}

	c.JSON(200, categories)
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
		`SELECT user_id FROM expense_categories WHERE id = $1`, categoryID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "category not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to update this category"})
		return
	}

	// Build update query dynamically
	query := `UPDATE expense_categories SET `
	args := []interface{}{}
	argCount := 1

	if req.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argCount)
		args = append(args, *req.Name)
		argCount++
	}
	if req.Color != nil {
		query += fmt.Sprintf("color = $%d, ", argCount)
		args = append(args, *req.Color)
		argCount++
	}
	if req.Icon != nil {
		query += fmt.Sprintf("icon = $%d, ", argCount)
		args = append(args, *req.Icon)
		argCount++
	}

	if argCount == 1 {
		c.JSON(400, gin.H{"error": "no fields to update"})
		return
	}

	// Remove trailing comma and add WHERE clause
	query = query[:len(query)-2] + fmt.Sprintf(" WHERE id = $%d RETURNING id, user_id, name, COALESCE(color, ''), COALESCE(icon, ''), created_at", argCount)
	args = append(args, categoryID)

	var category ExpenseCategory
	err = db.Pool.QueryRow(c.Request.Context(), query, args...).Scan(
		&category.ID, &category.UserID, &category.Name, &category.Color, &category.Icon, &category.CreatedAt)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to update category"})
		return
	}

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

	// Check if category belongs to user
	var ownerID uuid.UUID
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT user_id FROM expense_categories WHERE id = $1`, categoryID).Scan(&ownerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "category not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not authorized to delete this category"})
		return
	}

	_, err = db.Pool.Exec(c.Request.Context(),
		`DELETE FROM expense_categories WHERE id = $1`, categoryID)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to delete category"})
		return
	}

	c.JSON(200, gin.H{"message": "category deleted successfully"})
}
