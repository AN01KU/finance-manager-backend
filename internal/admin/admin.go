package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yanonymousV2/finance-manager-backend/internal/category"
)

const (
	sessionCookieName = "admin_session"
	sessionMaxAge     = 24 * 3600 // 24 hours
	pageSize          = 50
	maxLogEntries     = 200
)

// LogEntry represents a single request log entry.
type LogEntry struct {
	Time        string
	Method      string
	Path        string
	Status      int
	StatusClass string // "2xx", "4xx", "5xx"
	Duration    string
	DurationMs  float64
	IsSlow      bool // >500ms
	BodySize    int64
}

// LogStore is a thread-safe in-memory ring buffer for request logs.
type LogStore struct {
	mu      sync.Mutex
	entries []LogEntry
}

func NewLogStore() *LogStore {
	return &LogStore{entries: make([]LogEntry, 0, maxLogEntries)}
}

func (ls *LogStore) Add(entry LogEntry) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.entries) >= maxLogEntries {
		ls.entries = ls.entries[1:]
	}
	ls.entries = append(ls.entries, entry)
}

func (ls *LogStore) Get(method, statusFilter string) []LogEntry {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	// Return newest first.
	result := make([]LogEntry, 0, len(ls.entries))
	for i := len(ls.entries) - 1; i >= 0; i-- {
		e := ls.entries[i]
		if method != "" && e.Method != method {
			continue
		}
		if statusFilter == "slow" {
			if !e.IsSlow {
				continue
			}
		} else if statusFilter != "" && e.StatusClass != statusFilter {
			continue
		}
		result = append(result, e)
	}
	return result
}

func (ls *LogStore) Clear() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.entries = ls.entries[:0]
}

// LoggerMiddleware returns a Gin middleware that records requests into the LogStore.
func LoggerMiddleware(ls *LogStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		var statusClass string
		switch {
		case status >= 500:
			statusClass = "5xx"
		case status >= 400:
			statusClass = "4xx"
		default:
			statusClass = "2xx"
		}
		elapsed := time.Since(start)
		durationMs := float64(elapsed.Microseconds()) / 1000.0
		ls.Add(LogEntry{
			Time:        start.Format("15:04:05"),
			Method:      c.Request.Method,
			Path:        c.Request.URL.Path,
			Status:      status,
			StatusClass: statusClass,
			Duration:    elapsed.Round(time.Microsecond).String(),
			DurationMs:  durationMs,
			IsSlow:      durationMs > 500,
			BodySize:    c.Request.ContentLength,
		})
	}
}

// Admin holds all dependencies for the admin dashboard.
type Admin struct {
	pool     *pgxpool.Pool
	tmpls    map[string]*template.Template
	username string
	password string
	sessions map[string]time.Time
	mu       sync.Mutex
	logStore *LogStore
}

// New creates a new Admin instance.
func New(pool *pgxpool.Pool, username, password string, logStore *LogStore) *Admin {
	dir := filepath.Join("internal", "admin", "templates")
	layoutFile := filepath.Join(dir, "layout.html")

	pages := []string{"dashboard", "tables", "table_browse", "row_form", "sql", "logs", "users", "user_detail", "groups", "group_detail", "recurring", "audit", "search", "categories"}
	tmpls := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		pageFile := filepath.Join(dir, page+".html")
		tmpls[page] = template.Must(template.ParseFiles(layoutFile, pageFile))
	}
	// Login is standalone (no layout)
	tmpls["login"] = template.Must(template.ParseFiles(filepath.Join(dir, "login.html")))

	return &Admin{
		pool:     pool,
		tmpls:    tmpls,
		username: username,
		password: password,
		sessions: make(map[string]time.Time),
		logStore: logStore,
	}
}

// RegisterRoutes registers all admin routes on the given router group.
// rateLimiter is applied to the login POST to prevent brute-force attacks.
func (a *Admin) RegisterRoutes(r *gin.Engine, rateLimiter gin.HandlerFunc) {
	g := r.Group("/admin")

	// Public routes
	g.GET("/login", a.loginPage)
	g.POST("/login", rateLimiter, a.loginSubmit)

	// Protected routes
	protected := g.Group("/")
	protected.Use(a.authMiddleware())
	{
		protected.GET("/", a.dashboard)
		protected.GET("/tables", a.tables)
		protected.GET("/tables/:name", a.tableBrowse)
		protected.GET("/tables/:name/new", a.rowCreateForm)
		protected.POST("/tables/:name/new", a.rowCreateSubmit)
		protected.GET("/tables/:name/edit", a.rowEditForm)
		protected.POST("/tables/:name/edit", a.rowEditSubmit)
		protected.GET("/users", a.usersPage)
		protected.GET("/users/:id", a.userDetail)
		protected.POST("/users/:id/delete", a.userDelete)
		protected.GET("/groups", a.groupsPage)
		protected.GET("/groups/:id", a.groupDetail)
		protected.GET("/recurring", a.recurringPage)
		protected.GET("/audit", a.auditPage)
		protected.GET("/search", a.searchPage)
		protected.GET("/sql", a.sqlPage)
		protected.POST("/sql", a.sqlExec)
		protected.POST("/rows/delete", a.rowDelete)
		protected.GET("/logs", a.logsPage)
		protected.POST("/logs/clear", a.logsClear)
		protected.GET("/categories", a.categoriesPage)
		protected.POST("/categories/new", a.categoryCreate)
		protected.POST("/categories/:id/edit", a.categoryEdit)
		protected.POST("/categories/:id/hide", a.categoryHide)
		protected.POST("/categories/:id/unhide", a.categoryUnhide)
		protected.POST("/categories/:id/hard-delete", a.categoryHardDelete)
		protected.GET("/category-icons/:key", a.categoryIconServe)
		protected.GET("/logout", a.logout)
	}
}

// --- Auth ---

func (a *Admin) generateSession() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("admin: failed to generate session: %v", err)
		return ""
	}
	return hex.EncodeToString(b)
}

func (a *Admin) createSession() string {
	token := a.generateSession()
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(sessionMaxAge * time.Second)
	a.mu.Unlock()
	return token
}

func (a *Admin) validSession(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, token)
		return false
	}
	return true
}

// JSONAuthMiddleware is the JSON-API equivalent of the HTML authMiddleware:
// instead of redirecting to /admin/login on a missing/invalid session, it
// returns a 401 JSON error. Use this for admin JSON endpoints
// (e.g. /admin/predefined-categories) registered alongside the HTML admin
// panel so they share the same cookie-session auth.
func (a *Admin) JSONAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(sessionCookieName)
		if err != nil || !a.validSession(cookie) {
			c.AbortWithStatusJSON(401, gin.H{"error": "admin session required"})
			return
		}
		c.Next()
	}
}

func (a *Admin) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie(sessionCookieName)
		if err != nil || !a.validSession(cookie) {
			c.Redirect(http.StatusFound, "/admin/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a *Admin) loginPage(c *gin.Context) {
	if err := a.tmpls["login"].ExecuteTemplate(c.Writer, "login.html", gin.H{"Error": ""}); err != nil {
		c.String(500, "template error: %v", err)
	}
}

func (a *Admin) loginSubmit(c *gin.Context) {
	user := c.PostForm("username")
	pass := c.PostForm("password")
	usernameMatch := subtle.ConstantTimeCompare([]byte(user), []byte(a.username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(a.password)) == 1
	if usernameMatch && passwordMatch && a.password != "" {
		token := a.createSession()
		secure := gin.Mode() == gin.ReleaseMode
		c.SetCookie(sessionCookieName, token, sessionMaxAge, "/admin", "", secure, true)
		c.Redirect(http.StatusFound, "/admin/")
		return
	}
	if err := a.tmpls["login"].ExecuteTemplate(c.Writer, "login.html", gin.H{"Error": "Invalid credentials"}); err != nil {
		c.String(500, "template error: %v", err)
	}
}

func (a *Admin) logout(c *gin.Context) {
	cookie, _ := c.Cookie(sessionCookieName)
	if cookie != "" {
		a.mu.Lock()
		delete(a.sessions, cookie)
		a.mu.Unlock()
	}
	c.SetCookie(sessionCookieName, "", -1, "/admin", "", false, true)
	c.Redirect(http.StatusFound, "/admin/login")
}

// --- Dashboard ---

type tableInfo struct {
	Name     string
	RowCount int64
}

type dashStats struct {
	Tables         int
	TotalRows      int64
	Users          int64
	Groups         int64
	DBSize         string
	TxThisMonth    int64
	TxLastMonth    int64
	TxTrend        string // "up", "down", "flat"
	SpendThisMonth string
	ActiveUsers7d  int64
	ActiveUsers30d int64
	NewSignupsWeek int64
}

type topCategory struct {
	Name    string
	Total   string
	TxCount int64
}

func (a *Admin) getTableList(c *gin.Context) ([]tableInfo, error) {
	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []tableInfo
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		var count int64
		err := a.pool.QueryRow(c.Request.Context(),
			fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&count)
		if err != nil {
			count = 0
		}
		tables = append(tables, tableInfo{Name: name, RowCount: count})
	}
	return tables, nil
}

func (a *Admin) dashboard(c *gin.Context) {
	tables, err := a.getTableList(c)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}

	var totalRows int64
	for _, t := range tables {
		totalRows += t.RowCount
	}

	var userCount int64
	_ = a.pool.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM users`).Scan(&userCount)

	var groupCount int64
	_ = a.pool.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM groups WHERE is_deleted = FALSE`).Scan(&groupCount)

	var dbSize string
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&dbSize)

	// Transactions this month vs last month
	var txThisMonth, txLastMonth int64
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := monthStart.AddDate(0, -1, 0)
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM transactions WHERE is_deleted = FALSE AND date >= $1`, monthStart).Scan(&txThisMonth)
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM transactions WHERE is_deleted = FALSE AND date >= $1 AND date < $2`,
		lastMonthStart, monthStart).Scan(&txLastMonth)

	txTrend := "flat"
	if txThisMonth > txLastMonth {
		txTrend = "up"
	} else if txThisMonth < txLastMonth {
		txTrend = "down"
	}

	// Total spending this month
	var spendThisMonth float64
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE is_deleted = FALSE AND type = 'expense' AND date >= $1`,
		monthStart).Scan(&spendThisMonth)

	// Active users (7d / 30d) — users who created a transaction
	var activeUsers7d, activeUsers30d int64
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM transactions WHERE is_deleted = FALSE AND created_at >= $1`,
		now.AddDate(0, 0, -7)).Scan(&activeUsers7d)
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM transactions WHERE is_deleted = FALSE AND created_at >= $1`,
		now.AddDate(0, 0, -30)).Scan(&activeUsers30d)

	// New signups this week
	var newSignupsWeek int64
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE created_at >= $1`,
		now.AddDate(0, 0, -7)).Scan(&newSignupsWeek)

	// Top 5 categories by spend this month
	catRows, err := a.pool.Query(c.Request.Context(),
		`SELECT category, SUM(amount) AS total, COUNT(*) AS cnt
		 FROM transactions
		 WHERE is_deleted = FALSE AND type = 'expense' AND date >= $1
		 GROUP BY category ORDER BY total DESC LIMIT 5`, monthStart)
	var topCategories []topCategory
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var tc topCategory
			var total float64
			if catRows.Scan(&tc.Name, &total, &tc.TxCount) == nil {
				tc.Total = fmt.Sprintf("%.2f", total)
				topCategories = append(topCategories, tc)
			}
		}
	}

	stats := dashStats{
		Tables:         len(tables),
		TotalRows:      totalRows,
		Users:          userCount,
		Groups:         groupCount,
		DBSize:         dbSize,
		TxThisMonth:    txThisMonth,
		TxLastMonth:    txLastMonth,
		TxTrend:        txTrend,
		SpendThisMonth: fmt.Sprintf("%.2f", spendThisMonth),
		ActiveUsers7d:  activeUsers7d,
		ActiveUsers30d: activeUsers30d,
		NewSignupsWeek: newSignupsWeek,
	}

	a.render(c, "dashboard.html", gin.H{
		"Title":         "Dashboard",
		"Active":        "dashboard",
		"Tables":        tables,
		"Stats":         stats,
		"TopCategories": topCategories,
	})
}

// --- Users ---

type userRow struct {
	ID        string
	Email     string
	Username  string
	TxCount   int64
	CreatedAt string
}

type userDetailRow struct {
	ID             string
	Email          string
	Username       string
	TxCount        int64
	BudgetCount    int64
	GroupCount     int64
	RecurringCount int64
	CreatedAt      string
}

type recentTx struct {
	Type        string
	Amount      string
	Category    string
	Description string
	Date        string
}

func (a *Admin) usersPage(c *gin.Context) {
	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT u.id, u.email, u.username, u.created_at,
		        COUNT(t.id) AS tx_count
		 FROM users u
		 LEFT JOIN transactions t ON t.user_id = u.id AND t.is_deleted = FALSE
		 GROUP BY u.id
		 ORDER BY u.created_at DESC`)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}
	defer rows.Close()

	var users []userRow
	for rows.Next() {
		var u userRow
		var id [16]byte
		var createdAt time.Time
		if err := rows.Scan(&id, &u.Email, &u.Username, &createdAt, &u.TxCount); err != nil {
			continue
		}
		u.ID = formatValue(id)
		u.CreatedAt = createdAt.Format("2006-01-02")
		users = append(users, u)
	}

	a.render(c, "users.html", gin.H{
		"Title":  "Users",
		"Active": "users",
		"Users":  users,
	})
}

func (a *Admin) userDetail(c *gin.Context) {
	uid := c.Param("id")

	var u userDetailRow
	var id [16]byte
	var createdAt time.Time
	err := a.pool.QueryRow(c.Request.Context(),
		`SELECT u.id, u.email, u.username, u.created_at,
		        (SELECT COUNT(*) FROM transactions WHERE user_id = u.id AND is_deleted = FALSE),
		        (SELECT COUNT(*) FROM monthly_budgets WHERE user_id = u.id),
		        (SELECT COUNT(*) FROM group_members WHERE user_id = u.id),
		        (SELECT COUNT(*) FROM recurring_transactions WHERE user_id = u.id)
		 FROM users u WHERE u.id = $1`, uid).Scan(
		&id, &u.Email, &u.Username, &createdAt,
		&u.TxCount, &u.BudgetCount, &u.GroupCount, &u.RecurringCount)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/users")
		return
	}
	u.ID = formatValue(id)
	u.CreatedAt = createdAt.Format("2006-01-02 15:04")

	// Recent transactions
	txRows, err := a.pool.Query(c.Request.Context(),
		`SELECT type, amount, category, COALESCE(description, ''), date
		 FROM transactions
		 WHERE user_id = $1 AND is_deleted = FALSE
		 ORDER BY date DESC LIMIT 20`, uid)
	var recentTxs []recentTx
	if err == nil {
		defer txRows.Close()
		for txRows.Next() {
			var t recentTx
			var txDate time.Time
			var amount float64
			if txRows.Scan(&t.Type, &amount, &t.Category, &t.Description, &txDate) == nil {
				t.Amount = fmt.Sprintf("%.2f", amount)
				t.Date = txDate.Format("2006-01-02")
				recentTxs = append(recentTxs, t)
			}
		}
	}

	a.render(c, "user_detail.html", gin.H{
		"Title":              u.Username,
		"Active":             "users",
		"User":               u,
		"RecentTransactions": recentTxs,
		"Success":            c.Query("success"),
	})
}

func (a *Admin) userDelete(c *gin.Context) {
	uid := c.Param("id")
	_, err := a.pool.Exec(c.Request.Context(), `DELETE FROM users WHERE id = $1`, uid)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/users/"+uid+"?error="+url.QueryEscape(err.Error()))
		return
	}
	a.logAudit(c, "delete", "users", "User ID: "+uid)
	c.Redirect(http.StatusFound, "/admin/users?success=User+deleted")
}

// --- Groups ---

type groupRow struct {
	ID              string
	Name            string
	MemberCount     int64
	TxCount         int64
	SettlementCount int64
	TotalSpent      string
	CreatedByEmail  string
	CreatedAt       string
}

type groupMember struct {
	Username string
	Email    string
	JoinedAt string
}

type groupTx struct {
	PaidBy      string
	Amount      string
	Category    string
	Description string
	Date        string
}

type groupSettlement struct {
	FromUser  string
	ToUser    string
	Amount    string
	CreatedAt string
}

func (a *Admin) groupsPage(c *gin.Context) {
	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT g.id, g.name,
		        (SELECT COUNT(*) FROM group_members WHERE group_id = g.id),
		        (SELECT COUNT(*) FROM group_transactions WHERE group_id = g.id AND is_deleted = FALSE),
		        u.email, g.created_at
		 FROM groups g
		 JOIN users u ON u.id = g.created_by
		 WHERE g.is_deleted = FALSE
		 ORDER BY g.created_at DESC`)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}
	defer rows.Close()

	var groups []groupRow
	for rows.Next() {
		var g groupRow
		var id [16]byte
		var createdAt time.Time
		if err := rows.Scan(&id, &g.Name, &g.MemberCount, &g.TxCount, &g.CreatedByEmail, &createdAt); err != nil {
			continue
		}
		g.ID = formatValue(id)
		g.CreatedAt = createdAt.Format("2006-01-02")
		groups = append(groups, g)
	}

	a.render(c, "groups.html", gin.H{
		"Title":  "Groups",
		"Active": "groups",
		"Groups": groups,
	})
}

func (a *Admin) groupDetail(c *gin.Context) {
	gid := c.Param("id")

	var g groupRow
	var id [16]byte
	var createdAt time.Time
	var totalSpent float64
	err := a.pool.QueryRow(c.Request.Context(),
		`SELECT g.id, g.name,
		        (SELECT COUNT(*) FROM group_members WHERE group_id = g.id),
		        (SELECT COUNT(*) FROM group_transactions WHERE group_id = g.id AND is_deleted = FALSE),
		        (SELECT COUNT(*) FROM settlements WHERE group_id = g.id),
		        COALESCE((SELECT SUM(total_amount) FROM group_transactions WHERE group_id = g.id AND is_deleted = FALSE), 0),
		        u.email, g.created_at
		 FROM groups g
		 JOIN users u ON u.id = g.created_by
		 WHERE g.id = $1 AND g.is_deleted = FALSE`, gid).Scan(
		&id, &g.Name, &g.MemberCount, &g.TxCount, &g.SettlementCount,
		&totalSpent, &g.CreatedByEmail, &createdAt)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/groups")
		return
	}
	g.ID = formatValue(id)
	g.CreatedAt = createdAt.Format("2006-01-02")
	g.TotalSpent = fmt.Sprintf("%.2f", totalSpent)

	// Members
	memberRows, err := a.pool.Query(c.Request.Context(),
		`SELECT u.username, u.email, gm.joined_at
		 FROM group_members gm
		 JOIN users u ON u.id = gm.user_id
		 WHERE gm.group_id = $1
		 ORDER BY gm.joined_at`, gid)
	var members []groupMember
	if err == nil {
		defer memberRows.Close()
		for memberRows.Next() {
			var m groupMember
			var joinedAt time.Time
			if memberRows.Scan(&m.Username, &m.Email, &joinedAt) == nil {
				m.JoinedAt = joinedAt.Format("2006-01-02")
				members = append(members, m)
			}
		}
	}

	// Recent transactions
	txRows, err := a.pool.Query(c.Request.Context(),
		`SELECT u.username, gt.total_amount, gt.category, COALESCE(gt.description, ''), gt.date
		 FROM group_transactions gt
		 JOIN users u ON u.id = gt.paid_by_user_id
		 WHERE gt.group_id = $1 AND gt.is_deleted = FALSE
		 ORDER BY gt.date DESC LIMIT 20`, gid)
	var transactions []groupTx
	if err == nil {
		defer txRows.Close()
		for txRows.Next() {
			var t groupTx
			var amount float64
			var txDate time.Time
			if txRows.Scan(&t.PaidBy, &amount, &t.Category, &t.Description, &txDate) == nil {
				t.Amount = fmt.Sprintf("%.2f", amount)
				t.Date = txDate.Format("2006-01-02")
				transactions = append(transactions, t)
			}
		}
	}

	// Settlements
	settRows, err := a.pool.Query(c.Request.Context(),
		`SELECT fu.username, tu.username, s.amount, s.created_at
		 FROM settlements s
		 JOIN users fu ON fu.id = s.from_user
		 JOIN users tu ON tu.id = s.to_user
		 WHERE s.group_id = $1
		 ORDER BY s.created_at DESC LIMIT 20`, gid)
	var settlements []groupSettlement
	if err == nil {
		defer settRows.Close()
		for settRows.Next() {
			var s groupSettlement
			var amount float64
			var sCreatedAt time.Time
			if settRows.Scan(&s.FromUser, &s.ToUser, &amount, &sCreatedAt) == nil {
				s.Amount = fmt.Sprintf("%.2f", amount)
				s.CreatedAt = sCreatedAt.Format("2006-01-02")
				settlements = append(settlements, s)
			}
		}
	}

	a.render(c, "group_detail.html", gin.H{
		"Title":        g.Name,
		"Active":       "groups",
		"Group":        g,
		"Members":      members,
		"Transactions": transactions,
		"Settlements":  settlements,
	})
}

// --- Recurring ---

type recurringRule struct {
	UserEmail string
	Name      string
	Type      string
	Amount    string
	Category  string
	Frequency string
	LastAdded string
	IsActive  bool
	IsOverdue bool
}

func isOverdue(frequency string, lastAdded *time.Time, now time.Time) bool {
	if lastAdded == nil {
		return false
	}
	var next time.Time
	switch frequency {
	case "daily":
		next = lastAdded.AddDate(0, 0, 1)
	case "weekly":
		next = lastAdded.AddDate(0, 0, 7)
	case "monthly":
		next = lastAdded.AddDate(0, 1, 0)
	case "yearly":
		next = lastAdded.AddDate(1, 0, 0)
	default:
		return false
	}
	return now.After(next)
}

func (a *Admin) recurringPage(c *gin.Context) {
	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT u.email, rt.name, rt.type, rt.amount, rt.category,
		        rt.frequency, rt.last_added_date, rt.is_active
		 FROM recurring_transactions rt
		 JOIN users u ON u.id = rt.user_id
		 ORDER BY rt.is_active DESC, rt.updated_at DESC`)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	var rules []recurringRule
	for rows.Next() {
		var r recurringRule
		var amount float64
		var lastAdded *time.Time
		if err := rows.Scan(&r.UserEmail, &r.Name, &r.Type, &amount, &r.Category,
			&r.Frequency, &lastAdded, &r.IsActive); err != nil {
			continue
		}
		r.Amount = fmt.Sprintf("%.2f", amount)
		if lastAdded != nil {
			r.LastAdded = lastAdded.Format("2006-01-02")
		} else {
			r.LastAdded = "—"
		}
		r.IsOverdue = r.IsActive && isOverdue(r.Frequency, lastAdded, now)
		rules = append(rules, r)
	}

	a.render(c, "recurring.html", gin.H{
		"Title":  "Recurring Transactions",
		"Active": "recurring",
		"Rules":  rules,
	})
}

// --- Audit Log ---

func (a *Admin) logAudit(c *gin.Context, action, tableName, details string) {
	_, _ = a.pool.Exec(c.Request.Context(),
		`INSERT INTO admin_audit_log (action, table_name, details, admin_user) VALUES ($1, $2, $3, $4)`,
		action, tableName, details, a.username)
}

type auditEntry struct {
	CreatedAt string
	AdminUser string
	Action    string
	TableName string
	Details   string
}

func (a *Admin) auditPage(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT created_at, admin_user, action, COALESCE(table_name, ''), COALESCE(details, '')
		 FROM admin_audit_log
		 ORDER BY created_at DESC
		 LIMIT $1 OFFSET $2`, pageSize+1, offset)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}
	defer rows.Close()

	var entries []auditEntry
	for rows.Next() {
		var e auditEntry
		var createdAt time.Time
		if rows.Scan(&createdAt, &e.AdminUser, &e.Action, &e.TableName, &e.Details) == nil {
			e.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
			entries = append(entries, e)
		}
	}

	hasNext := len(entries) > pageSize
	if hasNext {
		entries = entries[:pageSize]
	}

	a.render(c, "audit.html", gin.H{
		"Title":    "Audit Log",
		"Active":   "audit",
		"Entries":  entries,
		"Page":     page,
		"PrevPage": page - 1,
		"NextPage": page + 1,
		"HasNext":  hasNext,
	})
}

// --- Search ---

type searchUser struct {
	ID        string
	Username  string
	Email     string
	CreatedAt string
}

type searchGroup struct {
	ID          string
	Name        string
	MemberCount int64
	CreatedAt   string
}

type searchTx struct {
	ID        string
	UserEmail string
	Type      string
	Amount    string
	Category  string
	Date      string
}

func (a *Admin) searchPage(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.Redirect(http.StatusFound, "/admin/")
		return
	}
	pattern := "%" + q + "%"

	// Search users
	var users []searchUser
	uRows, err := a.pool.Query(c.Request.Context(),
		`SELECT id, username, email, created_at FROM users
		 WHERE email ILIKE $1 OR username ILIKE $1
		 ORDER BY created_at DESC LIMIT 20`, pattern)
	if err == nil {
		defer uRows.Close()
		for uRows.Next() {
			var u searchUser
			var id [16]byte
			var createdAt time.Time
			if uRows.Scan(&id, &u.Username, &u.Email, &createdAt) == nil {
				u.ID = formatValue(id)
				u.CreatedAt = createdAt.Format("2006-01-02")
				users = append(users, u)
			}
		}
	}

	// Search groups
	var groups []searchGroup
	gRows, err := a.pool.Query(c.Request.Context(),
		`SELECT g.id, g.name,
		        (SELECT COUNT(*) FROM group_members WHERE group_id = g.id),
		        g.created_at
		 FROM groups g
		 WHERE g.is_deleted = FALSE AND g.name ILIKE $1
		 ORDER BY g.created_at DESC LIMIT 20`, pattern)
	if err == nil {
		defer gRows.Close()
		for gRows.Next() {
			var g searchGroup
			var id [16]byte
			var createdAt time.Time
			if gRows.Scan(&id, &g.Name, &g.MemberCount, &createdAt) == nil {
				g.ID = formatValue(id)
				g.CreatedAt = createdAt.Format("2006-01-02")
				groups = append(groups, g)
			}
		}
	}

	// Search transactions by ID or description
	var transactions []searchTx
	tRows, err := a.pool.Query(c.Request.Context(),
		`SELECT t.id, u.email, t.type, t.amount, t.category, t.date
		 FROM transactions t
		 JOIN users u ON u.id = t.user_id
		 WHERE t.is_deleted = FALSE
		   AND (t.id::text ILIKE $1 OR COALESCE(t.description, '') ILIKE $1 OR t.category ILIKE $1)
		 ORDER BY t.date DESC LIMIT 20`, pattern)
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var tx searchTx
			var id [16]byte
			var amount float64
			var txDate time.Time
			if tRows.Scan(&id, &tx.UserEmail, &tx.Type, &amount, &tx.Category, &txDate) == nil {
				tx.ID = formatValue(id)
				tx.Amount = fmt.Sprintf("%.2f", amount)
				tx.Date = txDate.Format("2006-01-02")
				transactions = append(transactions, tx)
			}
		}
	}

	a.render(c, "search.html", gin.H{
		"Title":        "Search: " + q,
		"Active":       "",
		"Query":        q,
		"Users":        users,
		"Groups":       groups,
		"Transactions": transactions,
	})
}

// --- Tables ---

func (a *Admin) tables(c *gin.Context) {
	tables, err := a.getTableList(c)
	if err != nil {
		c.String(500, "Error: %v", err)
		return
	}
	a.render(c, "tables.html", gin.H{
		"Title":  "Tables",
		"Active": "tables",
		"Tables": tables,
	})
}

func (a *Admin) tableBrowse(c *gin.Context) {
	tableName := c.Param("name")

	// Validate table name exists
	var exists bool
	err := a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, tableName).Scan(&exists)
	if err != nil || !exists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	// Get total rows
	var totalRows int64
	_ = a.pool.QueryRow(c.Request.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM %q`, tableName)).Scan(&totalRows)

	// Get all primary key columns (supports composite PKs like group_members)
	var pkColumns []string
	pkRows, err := a.pool.Query(c.Request.Context(),
		`SELECT a.attname FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = $1::regclass AND i.indisprimary
		 ORDER BY array_position(i.indkey, a.attnum)`, tableName)
	if err == nil {
		defer pkRows.Close()
		for pkRows.Next() {
			var col string
			if pkRows.Scan(&col) == nil {
				pkColumns = append(pkColumns, col)
			}
		}
	}
	if len(pkColumns) == 0 {
		pkColumns = []string{"id"}
	}

	// Get rows
	query := fmt.Sprintf(`SELECT * FROM %q ORDER BY 1 LIMIT %d OFFSET %d`, tableName, pageSize, offset)
	rows, err := a.pool.Query(c.Request.Context(), query)
	if err != nil {
		a.render(c, "table_browse.html", gin.H{
			"Title": tableName, "Active": "tables", "TableName": tableName,
			"Error": err.Error(), "TotalRows": totalRows,
		})
		return
	}
	defer rows.Close()

	columns := make([]string, 0)
	for _, fd := range rows.FieldDescriptions() {
		columns = append(columns, string(fd.Name))
	}

	// Find the index of each PK column in the result set
	pkIndices := make([]int, 0, len(pkColumns))
	for _, pk := range pkColumns {
		for i, col := range columns {
			if col == pk {
				pkIndices = append(pkIndices, i)
				break
			}
		}
	}

	var resultRows [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		row := make([]string, len(values))
		for i, v := range values {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = formatValue(v)
			}
		}
		resultRows = append(resultRows, row)
	}

	a.render(c, "table_browse.html", gin.H{
		"Title":     tableName,
		"Active":    "tables",
		"TableName": tableName,
		"Columns":   columns,
		"Rows":      resultRows,
		"PKColumns": pkColumns,
		"PKIndices": pkIndices,
		"TotalRows": totalRows,
		"Page":      page,
		"PrevPage":  page - 1,
		"NextPage":  page + 1,
		"HasNext":   int64(offset+pageSize) < totalRows,
		"Error":     c.Query("error"),
		"Success":   c.Query("success"),
	})
}

// --- Row Delete ---

func (a *Admin) rowDelete(c *gin.Context) {
	table := c.PostForm("table")
	pkCols := c.PostFormArray("pk_column")
	pkVals := c.PostFormArray("pk_value")

	if len(pkCols) == 0 || len(pkCols) != len(pkVals) {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	// Validate table exists
	var tableExists bool
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&tableExists)
	if !tableExists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	// Validate all columns exist in the catalog (prevents SQL injection)
	for _, col := range pkCols {
		var colExists bool
		_ = a.pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name=$1 AND column_name=$2
			)`, table, col).Scan(&colExists)
		if !colExists {
			c.Redirect(http.StatusFound, "/admin/tables")
			return
		}
	}

	// Build WHERE clause with all PK columns using identifier quoting
	whereParts := make([]string, len(pkCols))
	args := make([]interface{}, len(pkCols))
	for i, col := range pkCols {
		colType := pgCastType(c, a.pool, table, col)
		whereParts[i] = fmt.Sprintf("%s = $%d::%s", quoteIdent(col), i+1, colType)
		args[i] = pkVals[i]
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(table), strings.Join(whereParts, " AND "))
	_, err := a.pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		log.Printf("admin: delete error: %v", err)
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"?error="+url.QueryEscape(err.Error()))
		return
	}
	a.logAudit(c, "delete", table, fmt.Sprintf("PK: %v = %v", pkCols, pkVals))
	c.Redirect(http.StatusFound, "/admin/tables/"+table+"?success=Row+deleted")
}

// --- Row Create / Edit ---

func (a *Admin) rowCreateForm(c *gin.Context) {
	table := c.Param("name")
	cols, err := a.getTableColumns(c, table)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}
	a.render(c, "row_form.html", gin.H{
		"Title":     "Add Row — " + table,
		"Active":    "tables",
		"TableName": table,
		"Columns":   cols,
		"IsEdit":    false,
		"Error":     c.Query("error"),
	})
}

func (a *Admin) rowCreateSubmit(c *gin.Context) {
	table := c.PostForm("table")

	var tableExists bool
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&tableExists)
	if !tableExists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	cols, err := a.getTableColumns(c, table)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"?error="+url.QueryEscape(err.Error()))
		return
	}

	// Collect non-empty values from the form
	var insertCols []string
	var placeholders []string
	var args []interface{}
	idx := 1
	for _, col := range cols {
		val := c.PostForm("col_" + col.Name)
		if val == "" && col.HasDefault {
			continue // let the DB use its default
		}
		if val == "" && col.Nullable {
			insertCols = append(insertCols, quoteIdent(col.Name))
			placeholders = append(placeholders, "NULL")
			continue
		}
		if val == "" {
			continue // skip — DB will use default or error
		}
		insertCols = append(insertCols, quoteIdent(col.Name))
		placeholders = append(placeholders, fmt.Sprintf("$%d::%s", idx, col.CastType))
		args = append(args, val)
		idx++
	}

	if len(insertCols) == 0 {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"/new?error="+url.QueryEscape("No values provided"))
		return
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table),
		strings.Join(insertCols, ", "),
		strings.Join(placeholders, ", "))
	_, err = a.pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"/new?error="+url.QueryEscape(err.Error()))
		return
	}
	a.logAudit(c, "create", table, fmt.Sprintf("Columns: %v", insertCols))
	c.Redirect(http.StatusFound, "/admin/tables/"+table+"?success=Row+created")
}

func (a *Admin) rowEditForm(c *gin.Context) {
	table := c.Param("name")

	// Validate table exists
	var exists bool
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&exists)
	if !exists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	// Retrieve PK columns & values from query params
	pkCols := c.QueryArray("pk_column")
	pkVals := c.QueryArray("pk_value")
	if len(pkCols) == 0 || len(pkCols) != len(pkVals) {
		c.Redirect(http.StatusFound, "/admin/tables/"+table)
		return
	}

	// Build WHERE clause to fetch the row
	whereParts := make([]string, len(pkCols))
	args := make([]interface{}, len(pkCols))
	for i, col := range pkCols {
		colType := pgCastType(c, a.pool, table, col)
		whereParts[i] = fmt.Sprintf("%s = $%d::%s", quoteIdent(col), i+1, colType)
		args[i] = pkVals[i]
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT 1", quoteIdent(table), strings.Join(whereParts, " AND "))
	rows, err := a.pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"?error="+url.QueryEscape(err.Error()))
		return
	}
	defer rows.Close()

	if !rows.Next() {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"?error=Row+not+found")
		return
	}
	values, _ := rows.Values()

	// Build column-value pairs for the form
	type colVal struct {
		Name     string
		Value    string
		CastType string
	}
	var colValues []colVal
	fieldDescs := rows.FieldDescriptions()
	for i, fd := range fieldDescs {
		val := ""
		if values[i] != nil {
			val = formatValue(values[i])
		}
		colValues = append(colValues, colVal{Name: string(fd.Name), Value: val})
	}

	a.render(c, "row_form.html", gin.H{
		"Title":     "Edit Row — " + table,
		"Active":    "tables",
		"TableName": table,
		"ColValues": colValues,
		"PKColumns": pkCols,
		"PKValues":  pkVals,
		"IsEdit":    true,
		"Error":     c.Query("error"),
	})
}

func (a *Admin) rowEditSubmit(c *gin.Context) {
	table := c.PostForm("table")
	pkCols := c.PostFormArray("pk_column")
	pkVals := c.PostFormArray("pk_value")

	if len(pkCols) == 0 || len(pkCols) != len(pkVals) {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	var tableExists bool
	_ = a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&tableExists)
	if !tableExists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	cols, err := a.getTableColumns(c, table)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/tables/"+table+"?error="+url.QueryEscape(err.Error()))
		return
	}

	// Build SET clause from submitted form values
	var setParts []string
	var args []interface{}
	idx := 1
	for _, col := range cols {
		val := c.PostForm("col_" + col.Name)
		if val == "" && col.Nullable {
			setParts = append(setParts, fmt.Sprintf("%s = NULL", quoteIdent(col.Name)))
			continue
		}
		if val == "" {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = $%d::%s", quoteIdent(col.Name), idx, col.CastType))
		args = append(args, val)
		idx++
	}

	// Build WHERE clause from PKs
	whereParts := make([]string, len(pkCols))
	for i, col := range pkCols {
		colType := pgCastType(c, a.pool, table, col)
		whereParts[i] = fmt.Sprintf("%s = $%d::%s", quoteIdent(col), idx, colType)
		args = append(args, pkVals[i])
		idx++
	}

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		quoteIdent(table),
		strings.Join(setParts, ", "),
		strings.Join(whereParts, " AND "))
	_, err = a.pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		// Rebuild edit URL with PK params
		editURL := "/admin/tables/" + table + "/edit?"
		for i := range pkCols {
			if i > 0 {
				editURL += "&"
			}
			editURL += "pk_column=" + url.QueryEscape(pkCols[i]) + "&pk_value=" + url.QueryEscape(pkVals[i])
		}
		editURL += "&error=" + url.QueryEscape(err.Error())
		c.Redirect(http.StatusFound, editURL)
		return
	}
	a.logAudit(c, "edit", table, fmt.Sprintf("PK: %v = %v", pkCols, pkVals))
	c.Redirect(http.StatusFound, "/admin/tables/"+table+"?success=Row+updated")
}

// columnInfo holds metadata about a table column for form rendering.
type columnInfo struct {
	Name       string
	DataType   string
	CastType   string
	Nullable   bool
	HasDefault bool
}

func (a *Admin) getTableColumns(c *gin.Context, table string) ([]columnInfo, error) {
	// Validate table exists
	var exists bool
	err := a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&exists)
	if err != nil || !exists {
		return nil, fmt.Errorf("table not found")
	}

	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT column_name, data_type, is_nullable, column_default
		 FROM information_schema.columns
		 WHERE table_schema='public' AND table_name=$1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []columnInfo
	for rows.Next() {
		var name, dataType, nullable string
		var colDefault *string
		if err := rows.Scan(&name, &dataType, &nullable, &colDefault); err != nil {
			return nil, err
		}
		cols = append(cols, columnInfo{
			Name:       name,
			DataType:   dataType,
			CastType:   pgCastType(c, a.pool, table, name),
			Nullable:   nullable == "YES",
			HasDefault: colDefault != nil,
		})
	}
	return cols, nil
}

// --- SQL Runner ---

func (a *Admin) sqlPage(c *gin.Context) {
	a.render(c, "sql.html", gin.H{
		"Title":  "SQL Runner",
		"Active": "sql",
	})
}

func (a *Admin) sqlExec(c *gin.Context) {
	// Disable SQL execution in production for security
	if gin.Mode() == gin.ReleaseMode {
		a.render(c, "sql.html", gin.H{
			"Title": "SQL Runner", "Active": "sql",
			"Error": "SQL runner is disabled in production mode",
		})
		return
	}

	query := strings.TrimSpace(c.PostForm("query"))
	if query == "" {
		a.render(c, "sql.html", gin.H{
			"Title": "SQL Runner", "Active": "sql", "Error": "Query cannot be empty",
		})
		return
	}

	data := gin.H{
		"Title":  "SQL Runner",
		"Active": "sql",
		"Query":  query,
	}

	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	isSelect := strings.HasPrefix(upperQuery, "SELECT") ||
		strings.HasPrefix(upperQuery, "WITH") ||
		strings.HasPrefix(upperQuery, "EXPLAIN")

	if isSelect {
		rows, err := a.pool.Query(c.Request.Context(), query)
		if err != nil {
			data["Error"] = err.Error()
			a.render(c, "sql.html", data)
			return
		}
		defer rows.Close()

		columns := make([]string, 0)
		for _, fd := range rows.FieldDescriptions() {
			columns = append(columns, string(fd.Name))
		}

		var resultRows [][]string
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				continue
			}
			row := make([]string, len(values))
			for i, v := range values {
				if v == nil {
					row[i] = "NULL"
				} else {
					row[i] = formatValue(v)
				}
			}
			resultRows = append(resultRows, row)
		}

		data["Columns"] = columns
		data["Rows"] = resultRows
	} else {
		tag, err := a.pool.Exec(c.Request.Context(), query)
		if err != nil {
			data["Error"] = err.Error()
		} else {
			data["Success"] = fmt.Sprintf("Query executed successfully. Rows affected: %d", tag.RowsAffected())
			a.logAudit(c, "sql_exec", "", query)
		}
	}

	a.render(c, "sql.html", data)
}

// --- Logs ---

func (a *Admin) logsPage(c *gin.Context) {
	method := c.Query("method")
	statusFilter := c.Query("status")
	logs := a.logStore.Get(method, statusFilter)
	a.render(c, "logs.html", gin.H{
		"Title":        "Logs",
		"Active":       "logs",
		"Logs":         logs,
		"Filter":       method,
		"StatusFilter": statusFilter,
	})
}

func (a *Admin) logsClear(c *gin.Context) {
	a.logStore.Clear()
	c.Redirect(http.StatusFound, "/admin/logs")
}

// --- Helpers ---

// formatValue converts a pgx row value to a display string, handling types
// like [16]byte (UUID) that fmt.Sprintf("%v") renders as byte arrays.
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case [16]byte:
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			val[0:4], val[4:6], val[6:8], val[8:10], val[10:16])
	default:
		return fmt.Sprintf("%v", v)
	}
}

// quoteIdent wraps a SQL identifier in double quotes, escaping any embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// pgCastType returns a valid PostgreSQL cast type for a column.
// information_schema.data_type returns verbose names (e.g. "character varying",
// "timestamp with time zone") that are not valid in a ::cast expression, so we
// map them to their short aliases.
func pgCastType(c *gin.Context, pool *pgxpool.Pool, table, column string) string {
	var dataType string
	err := pool.QueryRow(c.Request.Context(),
		`SELECT data_type FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&dataType)
	if err != nil || dataType == "" {
		return "text"
	}
	switch strings.ToLower(dataType) {
	case "character varying":
		return "varchar"
	case "character":
		return "char"
	case "timestamp with time zone":
		return "timestamptz"
	case "timestamp without time zone":
		return "timestamp"
	case "double precision":
		return "float8"
	case "real":
		return "float4"
	case "smallint":
		return "int2"
	case "bigint":
		return "int8"
	case "boolean":
		return "bool"
	case "user-defined":
		// For custom/enum types, query the actual udt_name
		var udtName string
		_ = pool.QueryRow(c.Request.Context(),
			`SELECT udt_name FROM information_schema.columns
			 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
			table, column).Scan(&udtName)
		if udtName != "" {
			return udtName
		}
		return "text"
	case "array":
		return "text"
	default:
		return dataType
	}
}

// --- Predefined Categories ---

type catRow struct {
	ID          string
	Key         string
	Name        string
	Icon        string
	Color       string
	IsHidden    bool
	IsProtected bool
}

func (a *Admin) categoriesPage(c *gin.Context) {
	rows, err := a.pool.Query(c.Request.Context(),
		`SELECT id, key, name, icon, color, is_hidden
		   FROM predefined_categories
		  ORDER BY name ASC`)
	if err != nil {
		c.String(500, "DB error: %v", err)
		return
	}
	defer rows.Close()

	var cats []catRow
	for rows.Next() {
		var cr catRow
		var id [16]byte
		if err := rows.Scan(&id, &cr.Key, &cr.Name, &cr.Icon, &cr.Color, &cr.IsHidden); err != nil {
			continue
		}
		cr.ID = formatValue(id)
		cr.IsProtected = cr.Key == category.ProtectedKey
		cats = append(cats, cr)
	}

	a.render(c, "categories.html", gin.H{
		"Title":      "Categories",
		"Active":     "categories",
		"Categories": cats,
		"IconKeys":   category.ValidIconKeys(),
		"Success":    c.Query("success"),
		"Error":      c.Query("error"),
	})
}

// catRedirect sends the user back to /admin/categories with an optional
// success or error message attached as a query string.
func catRedirect(c *gin.Context, success, errMsg string) {
	q := url.Values{}
	if success != "" {
		q.Set("success", success)
	}
	if errMsg != "" {
		q.Set("error", errMsg)
	}
	dest := "/admin/categories"
	if encoded := q.Encode(); encoded != "" {
		dest += "?" + encoded
	}
	c.Redirect(http.StatusFound, dest)
}

func (a *Admin) categoryCreate(c *gin.Context) {
	key := strings.TrimSpace(c.PostForm("key"))
	name := strings.TrimSpace(c.PostForm("name"))
	icon := strings.TrimSpace(c.PostForm("icon"))
	color := strings.TrimSpace(c.PostForm("color"))

	if key == "" || name == "" || icon == "" || color == "" {
		catRedirect(c, "", "All fields are required")
		return
	}
	if !category.IsValidIconKey(icon) {
		catRedirect(c, "", "Invalid icon key: "+icon)
		return
	}

	_, err := a.pool.Exec(c.Request.Context(),
		`INSERT INTO predefined_categories (key, name, icon, color)
		 VALUES ($1, $2, $3, $4)`,
		key, name, icon, color)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			catRedirect(c, "", "Key already exists: "+key)
			return
		}
		catRedirect(c, "", "Failed to create: "+err.Error())
		return
	}
	catRedirect(c, "Created "+name, "")
}

func (a *Admin) categoryEdit(c *gin.Context) {
	id := c.Param("id")
	name := strings.TrimSpace(c.PostForm("name"))
	icon := strings.TrimSpace(c.PostForm("icon"))
	color := strings.TrimSpace(c.PostForm("color"))

	if name == "" || icon == "" || color == "" {
		catRedirect(c, "", "Name, icon and color are required")
		return
	}
	if !category.IsValidIconKey(icon) {
		catRedirect(c, "", "Invalid icon key: "+icon)
		return
	}

	tag, err := a.pool.Exec(c.Request.Context(),
		`UPDATE predefined_categories
		    SET name = $1, icon = $2, color = $3, updated_at = NOW()
		  WHERE id = $4`,
		name, icon, color, id)
	if err != nil {
		catRedirect(c, "", "Update failed: "+err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		catRedirect(c, "", "Category not found")
		return
	}
	catRedirect(c, "Updated "+name, "")
}

// categorySetHidden flips the is_hidden flag on a predefined category and
// rejects the change for the protected key.
func (a *Admin) categorySetHidden(c *gin.Context, hidden bool) {
	id := c.Param("id")

	var key string
	if err := a.pool.QueryRow(c.Request.Context(),
		`SELECT key FROM predefined_categories WHERE id = $1`, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			catRedirect(c, "", "Category not found")
			return
		}
		catRedirect(c, "", "Lookup failed: "+err.Error())
		return
	}
	if key == category.ProtectedKey && hidden {
		catRedirect(c, "", "The protected category \"other\" cannot be hidden")
		return
	}

	if _, err := a.pool.Exec(c.Request.Context(),
		`UPDATE predefined_categories SET is_hidden = $1, updated_at = NOW() WHERE id = $2`,
		hidden, id); err != nil {
		catRedirect(c, "", "Update failed: "+err.Error())
		return
	}
	if hidden {
		catRedirect(c, "Hid "+key, "")
	} else {
		catRedirect(c, "Unhid "+key, "")
	}
}

func (a *Admin) categoryHide(c *gin.Context)   { a.categorySetHidden(c, true) }
func (a *Admin) categoryUnhide(c *gin.Context) { a.categorySetHidden(c, false) }

func (a *Admin) categoryHardDelete(c *gin.Context) {
	id := c.Param("id")

	var key string
	if err := a.pool.QueryRow(c.Request.Context(),
		`SELECT key FROM predefined_categories WHERE id = $1`, id).Scan(&key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			catRedirect(c, "", "Category not found")
			return
		}
		catRedirect(c, "", "Lookup failed: "+err.Error())
		return
	}
	if key == category.ProtectedKey {
		catRedirect(c, "", "The protected category \"other\" cannot be deleted")
		return
	}

	tx, err := a.pool.Begin(c.Request.Context())
	if err != nil {
		catRedirect(c, "", "Failed to begin transaction")
		return
	}
	defer tx.Rollback(c.Request.Context())

	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM custom_categories WHERE is_predefined = TRUE AND predefined_key = $1`,
		key); err != nil {
		catRedirect(c, "", "Failed to delete user overrides: "+err.Error())
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`DELETE FROM predefined_categories WHERE id = $1`, id); err != nil {
		catRedirect(c, "", "Failed to delete predefined category: "+err.Error())
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		catRedirect(c, "", "Commit failed: "+err.Error())
		return
	}
	catRedirect(c, "Permanently deleted "+key, "")
}

// categoryIconServe serves the embedded SVG bytes for a given icon key. Used
// by the admin categories page to render icon previews. Unknown keys → 404.
func (a *Admin) categoryIconServe(c *gin.Context) {
	key := c.Param("key")
	data, ok := category.IconSVG(key)
	if !ok {
		c.Status(404)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(200, "image/svg+xml", data)
}

func (a *Admin) render(c *gin.Context, name string, data gin.H) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	// name is e.g. "dashboard.html" — strip .html to get template key
	key := strings.TrimSuffix(name, ".html")
	tmpl, ok := a.tmpls[key]
	if !ok {
		log.Printf("admin: unknown template %q", key)
		c.String(500, "Unknown template: %s", key)
		return
	}
	if err := tmpl.ExecuteTemplate(c.Writer, "layout", data); err != nil {
		log.Printf("admin: template error (%s): %v", name, err)
		c.String(500, "Template error: %v", err)
	}
}
