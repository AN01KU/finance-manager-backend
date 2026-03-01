package dashboard

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/helpers"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type CategorySpending struct {
	CategoryID   *uuid.UUID            `json:"category_id"`
	CategoryName *string               `json:"category_name"`
	TotalAmount  helpers.StringDecimal `json:"total_amount"`
	ExpenseCount int                   `json:"expense_count"`
}

type MonthlyDashboard struct {
	Month             int                    `json:"month"`
	Year              int                    `json:"year"`
	Budget            *helpers.StringDecimal `json:"budget"`
	TotalSpent        helpers.StringDecimal  `json:"total_spent"`
	RemainingBudget   *helpers.StringDecimal `json:"remaining_budget"`
	DaysInMonth       int                    `json:"days_in_month"`
	DaysElapsed       int                    `json:"days_elapsed"`
	DaysRemaining     int                    `json:"days_remaining"`
	DailyAverageSpent helpers.StringDecimal  `json:"daily_average_spent"`
	ProjectedSpending *helpers.StringDecimal `json:"projected_spending"`
	IsOverBudget      bool                   `json:"is_over_budget"`
	ExpenseCount      int                    `json:"expense_count"`
	CategoryBreakdown []CategorySpending     `json:"category_breakdown"`
}

func GetMonthlyDashboard(c *gin.Context, db *db.DB) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	monthStr := c.Query("month")
	yearStr := c.Query("year")

	now := time.Now()
	month := int(now.Month())
	year := now.Year()

	if monthStr != "" {
		if _, err := fmt.Sscanf(monthStr, "%d", &month); err != nil || month < 1 || month > 12 {
			c.JSON(400, gin.H{"error": "invalid month"})
			return
		}
	}
	if yearStr != "" {
		if _, err := fmt.Sscanf(yearStr, "%d", &year); err != nil || year < 2000 || year > 2100 {
			c.JSON(400, gin.H{"error": "invalid year"})
			return
		}
	}

	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	var budgetAmount decimal.Decimal
	var budget *helpers.StringDecimal
	err := db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(amount, 0) FROM monthly_budgets WHERE user_id = $1 AND month = $2 AND year = $3`,
		userID, month, year).Scan(&budgetAmount)
	if err == nil {
		budget = &helpers.StringDecimal{budgetAmount}
	}

	var totalSpent decimal.Decimal
	var expenseCount int
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount), 0), COUNT(*) 
		 FROM personal_expenses 
		 WHERE user_id = $1 AND expense_date >= $2 AND expense_date < $3`,
		userID, startDate, endDate).Scan(&totalSpent, &expenseCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to calculate total spent"})
		return
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT pe.category, pe.category, COALESCE(SUM(pe.amount), 0), COUNT(*) 
		 FROM personal_expenses pe 
		 WHERE pe.user_id = $1 AND pe.expense_date >= $2 AND pe.expense_date < $3 
		 GROUP BY pe.category 
		 ORDER BY SUM(pe.amount) DESC`,
		userID, startDate, endDate)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get category breakdown"})
		return
	}
	defer rows.Close()

	var categoryBreakdown []CategorySpending
	for rows.Next() {
		var cs CategorySpending
		var catName string
		if err := rows.Scan(&cs.CategoryID, &catName, &cs.TotalAmount, &cs.ExpenseCount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan category breakdown"})
			return
		}
		cs.CategoryName = &catName
		categoryBreakdown = append(categoryBreakdown, cs)
	}

	if categoryBreakdown == nil {
		categoryBreakdown = []CategorySpending{}
	}

	daysInMonth := endDate.AddDate(0, 0, -1).Day()
	var daysElapsed int
	var daysRemaining int

	if year == now.Year() && month == int(now.Month()) {
		daysElapsed = now.Day()
		daysRemaining = daysInMonth - daysElapsed
	} else if startDate.After(now) {
		daysElapsed = 0
		daysRemaining = daysInMonth
	} else {
		daysElapsed = daysInMonth
		daysRemaining = 0
	}

	dailyAverageSpent := helpers.StringDecimal{decimal.Zero}
	if daysElapsed > 0 {
		dailyAverageSpent = helpers.StringDecimal{totalSpent.Div(decimal.NewFromInt(int64(daysElapsed)))}
	}

	var remainingBudget *helpers.StringDecimal
	var projectedSpending *helpers.StringDecimal
	var isOverBudget bool

	if budget != nil {
		remaining := helpers.StringDecimal{budgetAmount.Sub(totalSpent)}
		remainingBudget = &remaining
		isOverBudget = budgetAmount.LessThan(totalSpent)

		if daysElapsed > 0 {
			projected := helpers.StringDecimal{dailyAverageSpent.Decimal.Mul(decimal.NewFromInt(int64(daysInMonth)))}
			projectedSpending = &projected
		}
	}

	if projectedSpending == nil {
		zero := helpers.StringDecimal{decimal.Zero}
		projectedSpending = &zero
	}

	dashboard := MonthlyDashboard{
		Month:             month,
		Year:              year,
		Budget:            budget,
		TotalSpent:        helpers.StringDecimal{totalSpent},
		RemainingBudget:   remainingBudget,
		DaysInMonth:       daysInMonth,
		DaysElapsed:       daysElapsed,
		DaysRemaining:     daysRemaining,
		DailyAverageSpent: dailyAverageSpent,
		ProjectedSpending: projectedSpending,
		IsOverBudget:      isOverBudget,
		ExpenseCount:      expenseCount,
		CategoryBreakdown: categoryBreakdown,
	}

	c.JSON(200, dashboard)
}
