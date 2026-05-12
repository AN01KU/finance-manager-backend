package budget

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

var validate = validator.New()

type SetBudgetRequest struct {
	Limit *float64 `json:"limit" validate:"omitempty,gte=0"`
}

// GetBudget returns the user's monthly budget scalar (null if unset).
func GetBudget(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var limit *float64
	var raw *decimal.Decimal
	if err := database.Pool.QueryRow(c.Request.Context(),
		`SELECT monthly_budget FROM users WHERE id = $1`, userID,
	).Scan(&raw); err != nil {
		c.JSON(500, gin.H{"error": "failed to retrieve budget"})
		return
	}
	if raw != nil {
		v := raw.InexactFloat64()
		limit = &v
	}

	c.JSON(200, gin.H{"limit": limit})
}

// SetBudget sets or clears the user's monthly budget. Send {"limit": null} to
// clear it. Requires syncGuard on the route registration.
func SetBudget(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req SetBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := validate.Struct(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var newLimit *decimal.Decimal
	if req.Limit != nil {
		d := decimal.NewFromFloat(*req.Limit)
		newLimit = &d
	}

	if _, err := database.Pool.Exec(c.Request.Context(),
		`UPDATE users SET monthly_budget = $1, updated_at = NOW() WHERE id = $2`,
		newLimit, userID,
	); err != nil {
		c.JSON(500, gin.H{"error": "failed to set budget"})
		return
	}

	var out *float64
	if newLimit != nil {
		v := newLimit.InexactFloat64()
		out = &v
	}
	c.JSON(200, gin.H{"limit": out})
}
