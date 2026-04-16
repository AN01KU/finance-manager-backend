package dashboard

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

type CategorySpending struct {
	Category     string  `json:"category"`
	TotalAmount  float64 `json:"total_amount"`
	ExpenseCount int     `json:"expense_count"`
}

type MonthlyDashboard struct {
	Month             int                `json:"month"`
	Year              int                `json:"year"`
	TotalExpenses     float64            `json:"total_expenses"`
	ExpenseCount      int                `json:"expense_count"`
	Budget            *float64           `json:"budget,omitempty"`
	RemainingBudget   *float64           `json:"remaining_budget,omitempty"`
	DaysInMonth       int                `json:"days_in_month"`
	DaysElapsed       int                `json:"days_elapsed"`
	DaysRemaining     int                `json:"days_remaining"`
	DailyAverageSpent float64            `json:"daily_average_spent"`
	ProjectedSpending *float64           `json:"projected_spending,omitempty"`
	IsOverBudget      bool               `json:"is_over_budget"`
	CategoryBreakdown []CategorySpending `json:"category_breakdown"`
}

func GetMonthlyDashboard(c *gin.Context, db *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
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
	var budget *float64
	err := db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(budget_limit, 0) FROM monthly_budgets WHERE user_id = $1 AND month = $2 AND year = $3`,
		userID, month, year).Scan(&budgetAmount)
	if err == nil {
		v := budgetAmount.InexactFloat64()
		budget = &v
	}

	var totalSpent decimal.Decimal
	var expenseCount int
	err = db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount), 0), COUNT(*)
		 FROM transactions
		 WHERE user_id = $1 AND type = 'expense' AND date >= $2 AND date < $3 AND is_deleted = false`,
		userID, startDate, endDate).Scan(&totalSpent, &expenseCount)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to calculate total spent"})
		return
	}

	rows, err := db.Pool.Query(c.Request.Context(),
		`SELECT t.category, COALESCE(SUM(t.amount), 0), COUNT(*)
		 FROM transactions t
		 WHERE t.user_id = $1 AND t.type = 'expense' AND t.date >= $2 AND t.date < $3 AND t.is_deleted = false
		 GROUP BY t.category
		 ORDER BY SUM(t.amount) DESC`,
		userID, startDate, endDate)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get category breakdown"})
		return
	}
	defer rows.Close()

	var categoryBreakdown []CategorySpending
	for rows.Next() {
		var cs CategorySpending
		if err := rows.Scan(&cs.Category, &cs.TotalAmount, &cs.ExpenseCount); err != nil {
			c.JSON(500, gin.H{"error": "failed to scan category breakdown"})
			return
		}
		categoryBreakdown = append(categoryBreakdown, cs)
	}
	if err := rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "database iteration failed"})
		return
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

	dailyAverageSpent := 0.0
	if daysElapsed > 0 {
		dailyAverageSpent = totalSpent.Div(decimal.NewFromInt(int64(daysElapsed))).InexactFloat64()
	}

	var remainingBudget *float64
	var projectedSpending *float64
	var isOverBudget bool

	if budget != nil {
		remaining := budgetAmount.Sub(totalSpent).InexactFloat64()
		remainingBudget = &remaining
		isOverBudget = budgetAmount.LessThan(totalSpent)

		if daysElapsed > 0 {
			projected := decimal.NewFromFloat(dailyAverageSpent).Mul(decimal.NewFromInt(int64(daysInMonth))).InexactFloat64()
			projectedSpending = &projected
		}
	}

	if projectedSpending == nil {
		zero := 0.0
		projectedSpending = &zero
	}

	dashboard := MonthlyDashboard{
		Month:             month,
		Year:              year,
		TotalExpenses:     totalSpent.InexactFloat64(),
		ExpenseCount:      expenseCount,
		Budget:            budget,
		RemainingBudget:   remainingBudget,
		DaysInMonth:       daysInMonth,
		DaysElapsed:       daysElapsed,
		DaysRemaining:     daysRemaining,
		DailyAverageSpent: dailyAverageSpent,
		ProjectedSpending: projectedSpending,
		IsOverBudget:      isOverBudget,
		CategoryBreakdown: categoryBreakdown,
	}

	c.JSON(200, dashboard)
}
