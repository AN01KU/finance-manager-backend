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
	"github.com/joho/godotenv"

	"github.com/yanonymousV2/finance-manager-backend/internal/auth"
	"github.com/yanonymousV2/finance-manager-backend/internal/budget"
	"github.com/yanonymousV2/finance-manager-backend/internal/category"
	"github.com/yanonymousV2/finance-manager-backend/internal/config"
	"github.com/yanonymousV2/finance-manager-backend/internal/dashboard"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/expense"
	"github.com/yanonymousV2/finance-manager-backend/internal/group"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/personalexpense"
	"github.com/yanonymousV2/finance-manager-backend/internal/settlement"
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
	log.Println("[MARKER] About to setup Gin router")

	// Setup Gin
	log.Println("Setting up Gin router...")
	r := gin.Default()
	log.Println("✓ Gin router created")

	// Add request logging middleware
	log.Println("  → Adding request logging middleware...")
	r.Use(middleware.RequestLogger())
	log.Println("  ✓ Request logging middleware added")

	// Add CORS middleware
	log.Println("  → Adding CORS middleware...")
	r.Use(middleware.CORS())
	log.Println("  ✓ CORS middleware added")

	// Health check endpoint
	log.Println("  → Setting up health check endpoint...")
	r.GET("/health", func(c *gin.Context) {
		// Check database connectivity
		if err := database.Pool.Ping(c.Request.Context()); err != nil {
			c.JSON(503, gin.H{"status": "unhealthy", "database": "disconnected"})
			return
		}
		c.JSON(200, gin.H{"status": "healthy", "database": "connected"})
	})
	log.Println("  ✓ Health check endpoint setup")

	// Create auth service with config
	log.Println("  → Creating auth service...")
	authService := &auth.AuthService{
		DB:        database,
		JWTSecret: cfg.JWTSecret,
	}
	log.Println("  ✓ Auth service created")

	// Auth routes with rate limiting
	log.Println("  → Setting up auth routes...")
	authLimited := r.Group("/auth")
	authLimited.Use(middleware.RateLimiter())
	{
		authLimited.POST("/signup", func(c *gin.Context) { auth.Signup(c, authService) })
		authLimited.POST("/login", func(c *gin.Context) { auth.Login(c, authService) })
	}
	log.Println("  ✓ Auth routes setup")

	// Protected routes
	log.Println("  → Setting up protected routes...")
	protected := r.Group("/")
	protected.Use(middleware.JWTAuth(cfg.JWTSecret))
	{
		// Groups
		protected.POST("/groups", func(c *gin.Context) { group.CreateGroup(c, database) })
		protected.GET("/groups", func(c *gin.Context) { group.GetUserGroups(c, database) })
		protected.GET("/groups/:id", func(c *gin.Context) { group.GetGroup(c, database) })
		protected.POST("/groups/:id/add-member", func(c *gin.Context) { group.AddMember(c, database) })
		protected.GET("/groups/:id/balances", func(c *gin.Context) { group.GetBalances(c, database) })

		// Group Expenses
		protected.POST("/expenses", func(c *gin.Context) { expense.CreateExpense(c, database) })
		protected.GET("/groups/:id/expenses", func(c *gin.Context) { expense.GetGroupExpenses(c, database) })
		protected.GET("/expenses", func(c *gin.Context) { expense.GetUserExpenses(c, database) })

		// Settlements
		protected.POST("/settlements", func(c *gin.Context) { settlement.CreateSettlement(c, database) })

		// Personal Finance - Budget
		protected.POST("/budget", func(c *gin.Context) { budget.SetMonthlyBudget(c, database) })
		protected.GET("/budget", func(c *gin.Context) { budget.GetMonthlyBudget(c, database) })
		protected.GET("/budgets", func(c *gin.Context) { budget.ListBudgets(c, database) })

		// Personal Finance - Categories
		protected.POST("/categories", func(c *gin.Context) { category.CreateCategory(c, database) })
		protected.GET("/categories", func(c *gin.Context) { category.ListCategories(c, database) })
		protected.PUT("/categories/:id", func(c *gin.Context) { category.UpdateCategory(c, database) })
		protected.DELETE("/categories/:id", func(c *gin.Context) { category.DeleteCategory(c, database) })

		// Personal Finance - Expenses
		protected.POST("/personal-expenses", func(c *gin.Context) { personalexpense.CreateExpense(c, database) })
		protected.GET("/personal-expenses", func(c *gin.Context) { personalexpense.ListExpenses(c, database) })
		protected.GET("/personal-expenses/:id", func(c *gin.Context) { personalexpense.GetExpense(c, database) })
		protected.PUT("/personal-expenses/:id", func(c *gin.Context) { personalexpense.UpdateExpense(c, database) })
		protected.DELETE("/personal-expenses/:id", func(c *gin.Context) { personalexpense.DeleteExpense(c, database) })

		// Personal Finance - Dashboard
		protected.GET("/dashboard/monthly", func(c *gin.Context) { dashboard.GetMonthlyDashboard(c, database) })
	}
	log.Println("  ✓ All protected routes setup")

	log.Println("✓ Router setup complete")

	// Create server with timeouts
	srv := &http.Server{
		Addr:           ":" + cfg.Port,
		Handler:        r,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	// Start server in a goroutine
	go func() {
		log.Println("==============================================")
		log.Printf("🚀 Server listening on http://localhost:%s", cfg.Port)
		log.Println("==============================================")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server:", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Graceful shutdown with 5 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited gracefully")
}
