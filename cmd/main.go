package main

import (
	"context"
	"log"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/yanonymousV2/finance-manager-backend/internal/auth"
	"github.com/yanonymousV2/finance-manager-backend/internal/config"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/expense"
	"github.com/yanonymousV2/finance-manager-backend/internal/group"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/settlement"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()

	// Connect to DB
	database, err := db.New(ctx, cfg.DBURL)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer database.Close()

	// Run migrations
	migrationsPath := filepath.Join("internal", "db", "migrations")
	if err := db.RunMigrations(ctx, cfg.DBURL, migrationsPath); err != nil {
		log.Fatal("Failed to run migrations:", err)
	}

	// Setup Gin
	r := gin.Default()

	// Create auth service with config
	authService := &auth.AuthService{
		DB:        database,
		JWTSecret: cfg.JWTSecret,
	}

	// Auth routes
	r.POST("/auth/signup", func(c *gin.Context) { auth.Signup(c, authService) })
	r.POST("/auth/login", func(c *gin.Context) { auth.Login(c, authService) })

	// Protected routes
	protected := r.Group("/")
	protected.Use(middleware.JWTAuth(cfg.JWTSecret))
	{
		// Groups
		protected.POST("/groups", func(c *gin.Context) { group.CreateGroup(c, database) })
		protected.POST("/groups/:id/add-member", func(c *gin.Context) { group.AddMember(c, database) })
		protected.GET("/groups/:id/balances", func(c *gin.Context) { group.GetBalances(c, database) })

		// Expenses
		protected.POST("/expenses", func(c *gin.Context) { expense.CreateExpense(c, database) })
		protected.GET("/groups/:id/expenses", func(c *gin.Context) { expense.GetGroupExpenses(c, database) })

		// Settlements
		protected.POST("/settlements", func(c *gin.Context) { settlement.CreateSettlement(c, database) })
	}

	log.Printf("Server starting on port %s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
