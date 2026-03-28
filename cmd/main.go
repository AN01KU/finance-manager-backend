package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/yanonymousV2/finance-manager-backend/internal/auth"
	"github.com/yanonymousV2/finance-manager-backend/internal/budget"
	"github.com/yanonymousV2/finance-manager-backend/internal/category"
	"github.com/yanonymousV2/finance-manager-backend/internal/config"
	"github.com/yanonymousV2/finance-manager-backend/internal/dashboard"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/group"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/recurring"
	"github.com/yanonymousV2/finance-manager-backend/internal/seed"
	"github.com/yanonymousV2/finance-manager-backend/internal/settlement"
	"github.com/yanonymousV2/finance-manager-backend/internal/transaction"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	cfg := config.Load()

	log.Println("==============================================")
	log.Println("Finance Manager Backend starting...")
	log.Println("==============================================")

	ctx := context.Background()

	// Connect to DB
	log.Println("Connecting to database...")
	database, err := db.New(ctx, cfg.DBURL)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer database.Close()
	log.Println("✓ Database connection established")

	// Run migrations
	log.Println("Running database migrations...")
	migrationsPath := filepath.Join("internal", "db", "migrations")
	if err := db.RunMigrations(ctx, cfg.DBURL, migrationsPath); err != nil {
		log.Fatal("Failed to run migrations:", err)
	}
	log.Println("✓ Migrations completed successfully")

	// Seed test data
	log.Println("Seeding test data...")
	if err := seed.Seed(ctx, database); err != nil {
		log.Printf("Warning: failed to seed test data: %v", err)
	}

	// Setup Gin
	r := gin.Default()
	r.Use(middleware.RequestLogger())
	r.Use(middleware.CORS())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		if err := database.Pool.Ping(c.Request.Context()); err != nil {
			c.JSON(503, gin.H{"status": "unhealthy", "database": "disconnected"})
			return
		}
		c.JSON(200, gin.H{"status": "healthy", "database": "connected"})
	})

	// Auth service
	authService := &auth.AuthService{
		DB:        database,
		JWTSecret: cfg.JWTSecret,
		OnSignup: func(ctx context.Context, userID uuid.UUID) {
			if err := category.SeedPredefinedCategories(ctx, database, userID); err != nil {
				log.Printf("Warning: failed to seed categories for user %s: %v", userID, err)
			}
		},
	}

	// Auth routes (rate limited)
	authLimited := r.Group("/auth")
	authLimited.Use(middleware.RateLimiter())
	{
		authLimited.POST("/signup", func(c *gin.Context) { auth.Signup(c, authService) })
		authLimited.POST("/login", func(c *gin.Context) { auth.Login(c, authService) })
	}

	// Protected routes
	protected := r.Group("/")
	protected.Use(middleware.JWTAuth(cfg.JWTSecret))
	{
		// User profile
		protected.GET("/me", func(c *gin.Context) { auth.GetMe(c, authService) })
		protected.DELETE("/me", func(c *gin.Context) { auth.DeleteMe(c, authService) })

		// Transactions (personal expenses + income)
		protected.POST("/transactions", func(c *gin.Context) { transaction.CreateTransaction(c, database) })
		protected.GET("/transactions", func(c *gin.Context) { transaction.ListTransactions(c, database) })
		protected.GET("/transactions/:id", func(c *gin.Context) { transaction.GetTransaction(c, database) })
		protected.PATCH("/transactions/:id", func(c *gin.Context) { transaction.UpdateTransaction(c, database) })
		protected.DELETE("/transactions/:id", func(c *gin.Context) { transaction.DeleteTransaction(c, database) })

		// Recurring Transactions
		protected.POST("/recurring-transactions", func(c *gin.Context) { recurring.CreateRecurringTransaction(c, database) })
		protected.GET("/recurring-transactions", func(c *gin.Context) { recurring.ListRecurringTransactions(c, database) })
		protected.GET("/recurring-transactions/:id", func(c *gin.Context) { recurring.GetRecurringTransaction(c, database) })
		protected.PUT("/recurring-transactions/:id", func(c *gin.Context) { recurring.UpdateRecurringTransaction(c, database) })
		protected.DELETE("/recurring-transactions/:id", func(c *gin.Context) { recurring.DeleteRecurringTransaction(c, database) })

		// Budgets
		protected.POST("/budgets", func(c *gin.Context) { budget.CreateBudget(c, database) })
		protected.GET("/budgets", func(c *gin.Context) { budget.ListBudgets(c, database) })
		protected.PUT("/budgets/:id", func(c *gin.Context) { budget.UpdateBudget(c, database) })
		protected.DELETE("/budgets/:id", func(c *gin.Context) { budget.DeleteBudget(c, database) })

		// Categories
		protected.POST("/categories", func(c *gin.Context) { category.CreateCategory(c, database) })
		protected.GET("/categories", func(c *gin.Context) { category.ListCategories(c, database) })
		protected.PUT("/categories/:id", func(c *gin.Context) { category.UpdateCategory(c, database) })
		protected.DELETE("/categories/:id", func(c *gin.Context) { category.DeleteCategory(c, database) })

		// Dashboard
		protected.GET("/dashboard/monthly", func(c *gin.Context) { dashboard.GetMonthlyDashboard(c, database) })

		// Groups
		protected.POST("/groups", func(c *gin.Context) { group.CreateGroup(c, database) })
		protected.GET("/groups", func(c *gin.Context) { group.GetUserGroups(c, database) })
		protected.GET("/groups/:id", func(c *gin.Context) { group.GetGroup(c, database) })
		protected.POST("/groups/:id/add-member", func(c *gin.Context) { group.AddMember(c, database) })
		protected.GET("/groups/:id/members", func(c *gin.Context) { group.GetMembers(c, database) })
		protected.GET("/groups/:id/balances", func(c *gin.Context) { group.GetBalances(c, database) })

		// Group Transactions
		protected.POST("/groups/:id/transactions", func(c *gin.Context) { group.CreateGroupTransaction(c, database) })
		protected.GET("/groups/:id/transactions", func(c *gin.Context) { group.ListGroupTransactions(c, database) })
		protected.GET("/groups/:id/transactions/:txId", func(c *gin.Context) { group.GetGroupTransaction(c, database) })
		protected.PATCH("/groups/:id/transactions/:txId", func(c *gin.Context) { group.UpdateGroupTransaction(c, database) })
		protected.DELETE("/groups/:id/transactions/:txId", func(c *gin.Context) { group.DeleteGroupTransaction(c, database) })

		// Settlements
		protected.POST("/settlements", func(c *gin.Context) { settlement.CreateSettlement(c, database) })
	}

	// Create server with timeouts
	srv := &http.Server{
		Addr:           ":" + cfg.Port,
		Handler:        r,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		log.Println("==============================================")
		log.Printf("🚀 Server listening on http://localhost:%s", cfg.Port)
		log.Println("==============================================")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server:", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	// Cleanup test data
	seed.Cleanup(context.Background(), database)

	log.Println("Server exited gracefully")
}
