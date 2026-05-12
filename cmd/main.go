package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/admin"
	"github.com/yanonymousV2/finance-manager-backend/internal/auth"
	"github.com/yanonymousV2/finance-manager-backend/internal/budget"
	"github.com/yanonymousV2/finance-manager-backend/internal/category"
	"github.com/yanonymousV2/finance-manager-backend/internal/config"
	"github.com/yanonymousV2/finance-manager-backend/internal/dashboard"
	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/email"
	"github.com/yanonymousV2/finance-manager-backend/internal/group"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
	"github.com/yanonymousV2/finance-manager-backend/internal/notify"
	"github.com/yanonymousV2/finance-manager-backend/internal/portal"
	"github.com/yanonymousV2/finance-manager-backend/internal/recurring"
	"github.com/yanonymousV2/finance-manager-backend/internal/seed"
	"github.com/yanonymousV2/finance-manager-backend/internal/settlement"
	"github.com/yanonymousV2/finance-manager-backend/internal/sync"
	"github.com/yanonymousV2/finance-manager-backend/internal/transaction"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	cfg := config.Load()
	if err := cfg.Validate(gin.Mode()); err != nil {
		log.Fatal("Config validation failed: ", err)
	}

	log.Println("==============================================")
	log.Println("Finance Manager Backend starting...")
	log.Println("==============================================")

	// Initialize Sentry for error tracking (optional — skipped if SENTRY_DSN is empty)
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      gin.Mode(),
			TracesSampleRate: 0.2,
		}); err != nil {
			log.Printf("Warning: Sentry initialization failed: %v", err)
		} else {
			defer sentry.Flush(2 * time.Second)
			log.Println("✓ Sentry error tracking enabled")
		}
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	// Connect to DB
	log.Println("Connecting to database...")
	database, err := db.New(ctx, cfg.DBURL, cfg.DBMaxConns, cfg.DBMinConns)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	log.Println("✓ Database connection established")

	// Run migrations — use embedded FS by default; override with MIGRATION_PATH env var.
	log.Println("Running database migrations...")
	migrationsPath := os.Getenv("MIGRATION_PATH")
	if err := db.RunMigrations(ctx, cfg.DBURL, migrationsPath); err != nil {
		log.Fatal("Failed to run migrations:", err)
	}
	log.Println("✓ Migrations completed successfully")

	// Seed test data (development only)
	if gin.Mode() != gin.ReleaseMode {
		log.Println("Seeding test data...")
		if err := seed.Seed(ctx, database); err != nil {
			log.Printf("Warning: failed to seed test data: %v", err)
		}
	}

	// Start background jobs
	sync.StartSessionCleanup(ctx, database, cfg.SyncSessionTTLDays)
	recurring.StartBackgroundGeneration(ctx, database)
	transaction.StartSoftDeleteCleanup(ctx, database, cfg.TombstoneRetentionDays)

	// Initialize push notification client (optional — disabled if PUSHY_API_KEY is empty)
	pushClient := notify.New(cfg.PushyAPIKey, database.Pool)
	if pushClient.Enabled() {
		log.Println("✓ Push notifications enabled (Pushy)")
	}

	// Start settlement reminder background job
	notify.StartSettlementReminders(ctx, database, pushClient, notify.ReminderConfig{
		ThresholdAmount: decimal.NewFromFloat(cfg.ReminderThresholdAmount),
		DaysOutstanding: cfg.ReminderDaysOutstanding,
	})

	// Setup Gin
	r := gin.Default()
	// Disable trusted proxy headers unless explicitly configured.
	// Set TRUSTED_PROXIES env var to a comma-separated CIDR list when behind a load balancer.
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Fatal("Failed to set trusted proxies:", err)
	}
	r.Use(middleware.BodyLimit(1 << 20)) // 1 MiB max request body
	r.Use(middleware.SecurityHeaders())
	if cfg.SentryDSN != "" {
		r.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	}
	r.Use(middleware.RequestLogger())
	r.Use(middleware.CORS(cfg.CORSOrigin))

	// Admin dashboard with request logging
	logStore := admin.NewLogStore()
	r.Use(admin.LoggerMiddleware(logStore))
	adminPanel := admin.New(database.Pool, cfg.AdminUsername, cfg.AdminPassword, logStore)
	adminPanel.RegisterRoutes(r, middleware.RateLimiter())

	// User-facing portal at /dashboard
	portalApp := portal.New(database.Pool, cfg.JWTSecret)
	portalApp.RegisterRoutes(r)

	// Root redirects to the dashboard
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/dashboard/")
	})

	// Health check
	r.GET("/health", func(c *gin.Context) {
		if err := database.Pool.Ping(c.Request.Context()); err != nil {
			c.JSON(503, gin.H{"status": "unhealthy", "database": "disconnected"})
			return
		}
		c.JSON(200, gin.H{"status": "healthy", "database": "connected"})
	})

	// Public predefined categories list (no auth, no sync guard).
	r.GET("/predefined-categories", func(c *gin.Context) { category.GetPredefinedCategoriesHandler(c, database) })

	// Email client (optional — disabled if RESEND_API_KEY is empty)
	emailClient := email.New(cfg.ResendAPIKey, cfg.FromEmail)
	if emailClient.Enabled() {
		log.Println("✓ Email delivery enabled (Resend)")
	}

	// JWT-revocation cache: ~10s TTL skips the per-request SELECT against
	// users.tokens_invalidated_after on the warm path. Every code path that
	// bumps the column must call jwtCache.Invalidate(userID).
	jwtCache := middleware.NewJWTRevocationCache(10 * time.Second)

	// Auth service
	authService := &auth.AuthService{
		DB:          database,
		JWTSecret:   cfg.JWTSecret,
		InviteCode:  cfg.InviteCode,
		EmailClient: emailClient,
		JWTCache:    jwtCache,
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
	protected.Use(middleware.JWTAuth(cfg.JWTSecret, database, jwtCache))
	syncGuard := sync.SyncSessionGuard(database)
	{
		// Auth (protected)
		protected.POST("/auth/logout", func(c *gin.Context) { auth.Logout(c, authService) })
		protected.POST("/auth/verify-email", func(c *gin.Context) { auth.VerifyEmail(c, authService) })
		protected.POST("/auth/resend-verification", func(c *gin.Context) { auth.ResendVerification(c, authService) })

		// User profile
		protected.GET("/me", func(c *gin.Context) { auth.GetMe(c, authService) })
		protected.PATCH("/me", syncGuard, func(c *gin.Context) { auth.UpdateMe(c, authService) })
		protected.DELETE("/me", syncGuard, func(c *gin.Context) { auth.DeleteMe(c, authService) })

		// Transactions (personal expenses + income)
		protected.POST("/transactions", syncGuard, func(c *gin.Context) { transaction.CreateTransaction(c, database) })
		protected.GET("/transactions", func(c *gin.Context) { transaction.ListTransactions(c, database) })
		protected.GET("/transactions/export", func(c *gin.Context) { transaction.ExportTransactionsCSV(c, database) })
		protected.GET("/transactions/:id", func(c *gin.Context) { transaction.GetTransaction(c, database) })
		protected.PATCH("/transactions/:id", syncGuard, func(c *gin.Context) { transaction.UpdateTransaction(c, database) })
		protected.DELETE("/transactions/:id", syncGuard, func(c *gin.Context) { transaction.DeleteTransaction(c, database) })

		// Recurring Transactions
		protected.POST("/recurring-transactions", syncGuard, func(c *gin.Context) { recurring.CreateRecurringTransaction(c, database) })
		protected.GET("/recurring-transactions", func(c *gin.Context) { recurring.ListRecurringTransactions(c, database) })
		protected.GET("/recurring-transactions/:id", func(c *gin.Context) { recurring.GetRecurringTransaction(c, database) })
		protected.PATCH("/recurring-transactions/:id", syncGuard, func(c *gin.Context) { recurring.UpdateRecurringTransaction(c, database) })
		protected.DELETE("/recurring-transactions/:id", syncGuard, func(c *gin.Context) { recurring.DeleteRecurringTransaction(c, database) })

		// Budget (per-user scalar)
		protected.GET("/me/budget", func(c *gin.Context) { budget.GetBudget(c, database) })
		protected.PUT("/me/budget", syncGuard, func(c *gin.Context) { budget.SetBudget(c, database) })

		// Categories
		protected.POST("/categories", syncGuard, func(c *gin.Context) { category.CreateCategory(c, database) })
		protected.GET("/categories", func(c *gin.Context) { category.ListCategories(c, database) })
		protected.PATCH("/categories/:id", syncGuard, func(c *gin.Context) { category.UpdateCategory(c, database) })
		protected.DELETE("/categories/:id", syncGuard, func(c *gin.Context) { category.DeleteCategory(c, database) })

		// Dashboard
		protected.GET("/dashboard/monthly", func(c *gin.Context) { dashboard.GetMonthlyDashboard(c, database) })

		// Groups
		protected.POST("/groups", syncGuard, func(c *gin.Context) { group.CreateGroup(c, database) })
		protected.GET("/groups", func(c *gin.Context) { group.GetUserGroups(c, database) })
		protected.GET("/groups/:id", func(c *gin.Context) { group.GetGroup(c, database) })
		protected.PATCH("/groups/:id", syncGuard, func(c *gin.Context) { group.UpdateGroup(c, database) })
		protected.DELETE("/groups/:id", syncGuard, func(c *gin.Context) { group.DeleteGroup(c, database) })
		protected.POST("/groups/:id/members", syncGuard, func(c *gin.Context) { group.AddMember(c, database) })
		protected.GET("/groups/:id/members", func(c *gin.Context) { group.GetMembers(c, database) })
		protected.DELETE("/groups/:id/members/:userId", syncGuard, func(c *gin.Context) { group.RemoveMember(c, database) })
		protected.POST("/groups/:id/leave", syncGuard, func(c *gin.Context) { group.LeaveGroup(c, database) })
		protected.GET("/groups/:id/balances", func(c *gin.Context) { group.GetBalances(c, database) })
		protected.GET("/groups/:id/settlements", func(c *gin.Context) { group.GetGroupSettlements(c, database) })

		// Group Transactions
		protected.POST("/groups/:id/transactions", syncGuard, func(c *gin.Context) { group.CreateGroupTransaction(c, database) })
		protected.GET("/groups/:id/transactions", func(c *gin.Context) { group.ListGroupTransactions(c, database) })
		protected.GET("/groups/:id/transactions/:txId", func(c *gin.Context) { group.GetGroupTransaction(c, database) })
		protected.PATCH("/groups/:id/transactions/:txId", syncGuard, func(c *gin.Context) { group.UpdateGroupTransaction(c, database) })
		protected.DELETE("/groups/:id/transactions/:txId", syncGuard, func(c *gin.Context) { group.DeleteGroupTransaction(c, database) })

		// Settlements
		protected.POST("/settlements", syncGuard, func(c *gin.Context) { settlement.CreateSettlement(c, database) })
		protected.GET("/settlements/:id", func(c *gin.Context) { settlement.GetSettlement(c, database) })
		protected.DELETE("/settlements/:id", syncGuard, func(c *gin.Context) { settlement.DeleteSettlement(c, database) })
		protected.PATCH("/settlements/:id", syncGuard, func(c *gin.Context) { settlement.UpdateSettlement(c, database) })

		// Device tokens (push notifications)
		protected.POST("/device-tokens", syncGuard, func(c *gin.Context) { notify.RegisterToken(c, database.Pool) })
		protected.DELETE("/device-tokens", syncGuard, func(c *gin.Context) { notify.UnregisterToken(c, database.Pool) })

		// Sync
		protected.POST("/sync/preflight", func(c *gin.Context) { sync.Preflight(c, database) })
	}

	// Create server with timeouts
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
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

	// Correct shutdown order: cancel background jobs first, then drain HTTP,
	// then close DB so no background goroutine touches the pool after Close().
	ctxCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	// Cleanup test data (development only)
	if gin.Mode() != gin.ReleaseMode {
		seed.Cleanup(context.Background(), database)
	}

	database.Close()
	log.Println("Server exited gracefully")
}
