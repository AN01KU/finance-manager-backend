package portal

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/user"
)

const (
	cookieName = "portal_session"
	cookieTTL  = 7 * 24 * 3600 // 7 days
	pageSize   = 50
)

// Portal serves the user-facing web dashboard.
type Portal struct {
	pool      *pgxpool.Pool
	jwtSecret string
	tmpls     map[string]*template.Template
}

// New creates a Portal instance and pre-parses all templates.
func New(pool *pgxpool.Pool, jwtSecret string) *Portal {
	dir := filepath.Join("internal", "portal", "templates")
	layout := filepath.Join(dir, "layout.html")

	pages := []string{"dashboard", "transactions", "groups", "group_detail"}
	tmpls := make(map[string]*template.Template, len(pages)+1)
	for _, page := range pages {
		tmpls[page] = template.Must(template.ParseFiles(layout, filepath.Join(dir, page+".html")))
	}
	tmpls["login"] = template.Must(template.ParseFiles(filepath.Join(dir, "login.html")))

	return &Portal{pool: pool, jwtSecret: jwtSecret, tmpls: tmpls}
}

// RegisterRoutes mounts all portal routes on r.
func (p *Portal) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/dashboard")

	g.GET("/login", p.loginPage)
	g.POST("/login", p.loginSubmit)

	auth := g.Group("/")
	auth.Use(p.authMiddleware())
	{
		auth.GET("/", p.dashboardPage)
		auth.GET("/transactions", p.transactionsPage)
		auth.GET("/transactions/export", p.transactionsExport)
		auth.GET("/groups", p.groupsPage)
		auth.GET("/groups/:id", p.groupDetailPage)
		auth.GET("/logout", p.logout)
	}
}

// ── Auth ─────────────────────────────────────────────────────────────────────

func (p *Portal) loginPage(c *gin.Context) {
	// If already logged in redirect to dashboard.
	if tok, err := c.Cookie(cookieName); err == nil && p.validateJWT(tok) != uuid.Nil {
		c.Redirect(http.StatusFound, "/dashboard/")
		return
	}
	p.renderLogin(c, "")
}

func (p *Portal) loginSubmit(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	password := c.PostForm("password")

	if email == "" || password == "" {
		p.renderLogin(c, "Email and password are required.")
		return
	}

	// Look up user
	var u user.User
	var passwordHash string
	err := p.pool.QueryRow(c.Request.Context(),
		`SELECT id, email, username, password_hash FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Username, &passwordHash)
	if err != nil {
		// Constant-time compare to prevent timing attacks even on missing user
		_ = subtle.ConstantTimeCompare([]byte(password), []byte("$2a$10$placeholder_hash_value_here"))
		p.renderLogin(c, "Invalid email or password.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		p.renderLogin(c, "Invalid email or password.")
		return
	}

	// Issue JWT stored in cookie
	token, err := p.issueJWT(u.ID, u.Email)
	if err != nil {
		p.renderLogin(c, "Internal error, please try again.")
		return
	}

	secure := gin.Mode() == gin.ReleaseMode
	c.SetCookie(cookieName, token, cookieTTL, "/dashboard", "", secure, true)
	c.Redirect(http.StatusFound, "/dashboard/")
}

func (p *Portal) logout(c *gin.Context) {
	c.SetCookie(cookieName, "", -1, "/dashboard", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard/login")
}

func (p *Portal) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok, err := c.Cookie(cookieName)
		if err != nil {
			c.Redirect(http.StatusFound, "/dashboard/login")
			c.Abort()
			return
		}
		userID := p.validateJWT(tok)
		if userID == uuid.Nil {
			c.Redirect(http.StatusFound, "/dashboard/login")
			c.Abort()
			return
		}
		c.Set("portal_user_id", userID)
		c.Next()
	}
}

func (p *Portal) issueJWT(userID uuid.UUID, email string) (string, error) {
	claims := user.Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(p.jwtSecret))
}

func (p *Portal) validateJWT(tokenStr string) uuid.UUID {
	tok, err := jwt.ParseWithClaims(tokenStr, &user.Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(p.jwtSecret), nil
	})
	if err != nil || !tok.Valid {
		return uuid.Nil
	}
	claims, ok := tok.Claims.(*user.Claims)
	if !ok {
		return uuid.Nil
	}
	return claims.UserID
}

func (p *Portal) currentUser(c *gin.Context) (uuid.UUID, string, string) {
	uid, _ := c.Get("portal_user_id")
	userID, _ := uid.(uuid.UUID)

	var username, email string
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT username, email FROM users WHERE id = $1`, userID,
	).Scan(&username, &email)

	return userID, username, email
}

// ── Dashboard ────────────────────────────────────────────────────────────────

type dashCategoryRow struct {
	Category     string
	TotalAmount  string
	ExpenseCount int
}

type dashGroupBalance struct {
	GroupID     string
	GroupName   string
	MemberCount int
	AmountAbs   string
	IsPositive  bool
	IsNegative  bool
}

type dashRecentTx struct {
	Date        string
	Description string
	Category    string
	Type        string
	Amount      string
}

func (p *Portal) dashboardPage(c *gin.Context) {
	userID, username, email := p.currentUser(c)
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Total expenses this month
	var totalExpenses float64
	var expenseCount int
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0), COUNT(*) FROM transactions
		 WHERE user_id=$1 AND type='expense' AND date>=$2 AND date<$3 AND is_deleted=FALSE`,
		userID, monthStart, monthEnd).Scan(&totalExpenses, &expenseCount)

	// Total income this month
	var totalIncome float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM transactions
		 WHERE user_id=$1 AND type='income' AND date>=$2 AND date<$3 AND is_deleted=FALSE`,
		userID, monthStart, monthEnd).Scan(&totalIncome)

	// Budget
	var budgetLimit float64
	hasBudget := false
	err := p.pool.QueryRow(c.Request.Context(),
		`SELECT budget_limit FROM monthly_budgets WHERE user_id=$1 AND month=$2 AND year=$3`,
		userID, int(now.Month()), now.Year()).Scan(&budgetLimit)
	if err == nil {
		hasBudget = true
	}

	remainingBudget := budgetLimit - totalExpenses
	isOverBudget := hasBudget && totalExpenses > budgetLimit
	budgetPct := 0
	if hasBudget && budgetLimit > 0 {
		pct := (totalExpenses / budgetLimit) * 100
		if pct > 100 {
			pct = 100
		}
		budgetPct = int(pct)
	}

	// Category breakdown
	catRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT category, COALESCE(SUM(amount),0), COUNT(*) FROM transactions
		 WHERE user_id=$1 AND type='expense' AND date>=$2 AND date<$3 AND is_deleted=FALSE
		 GROUP BY category ORDER BY SUM(amount) DESC LIMIT 8`,
		userID, monthStart, monthEnd)
	var categories []dashCategoryRow
	if catRows != nil {
		defer catRows.Close()
		for catRows.Next() {
			var r dashCategoryRow
			var total float64
			if catRows.Scan(&r.Category, &total, &r.ExpenseCount) == nil {
				r.TotalAmount = fmt.Sprintf("%.2f", total)
				categories = append(categories, r)
			}
		}
	}

	// Group balances
	groupRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT g.id, g.name,
		        (SELECT COUNT(*) FROM group_members WHERE group_id=g.id)
		 FROM groups g
		 JOIN group_members gm ON gm.group_id=g.id
		 WHERE gm.user_id=$1 AND g.is_deleted=FALSE
		 ORDER BY g.created_at DESC`, userID)
	var groupBalances []dashGroupBalance
	var netOwed, netOwing float64
	if groupRows != nil {
		defer groupRows.Close()
		for groupRows.Next() {
			var gid uuid.UUID
			var gname string
			var memberCount int
			if groupRows.Scan(&gid, &gname, &memberCount) != nil {
				continue
			}
			balance := p.computeGroupBalance(c, gid, userID)
			gb := dashGroupBalance{
				GroupID:     gid.String(),
				GroupName:   gname,
				MemberCount: memberCount,
				AmountAbs:   fmt.Sprintf("%.2f", math.Abs(balance)),
				IsPositive:  balance > 0.005,
				IsNegative:  balance < -0.005,
			}
			if balance > 0.005 {
				netOwed += balance
			} else if balance < -0.005 {
				netOwing += -balance
			}
			groupBalances = append(groupBalances, gb)
		}
	}

	// Recent transactions
	txRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT type, COALESCE(amount,0), category, COALESCE(description,''), date FROM transactions
		 WHERE user_id=$1 AND is_deleted=FALSE
		 ORDER BY date DESC, created_at DESC LIMIT 8`, userID)
	var recentTxs []dashRecentTx
	if txRows != nil {
		defer txRows.Close()
		for txRows.Next() {
			var r dashRecentTx
			var amount float64
			var date time.Time
			if txRows.Scan(&r.Type, &amount, &r.Category, &r.Description, &date) == nil {
				r.Amount = fmt.Sprintf("%.2f", amount)
				r.Date = date.Format("Jan 2")
				recentTxs = append(recentTxs, r)
			}
		}
	}

	p.render(c, "dashboard", gin.H{
		"Title":              "Dashboard",
		"Active":             "dashboard",
		"Username":           username,
		"Email":              email,
		"MonthName":          now.Month().String(),
		"Year":               now.Year(),
		"TotalExpenses":      fmt.Sprintf("%.2f", totalExpenses),
		"TotalIncome":        fmt.Sprintf("%.2f", totalIncome),
		"ExpenseCount":       expenseCount,
		"HasBudget":          hasBudget,
		"RemainingBudget":    fmt.Sprintf("%.2f", remainingBudget),
		"BudgetPct":          budgetPct,
		"IsOverBudget":       isOverBudget,
		"NetOwed":            fmt.Sprintf("%.2f", netOwed),
		"NetOwing":           fmt.Sprintf("%.2f", netOwing),
		"CategoryBreakdown":  categories,
		"GroupBalances":      groupBalances,
		"RecentTransactions": recentTxs,
	})
}

// ── Transactions ─────────────────────────────────────────────────────────────

type portalTx struct {
	Date        string
	Description string
	Category    string
	GroupID     string
	GroupName   string
	Type        string
	Amount      string
}

func (p *Portal) transactionsPage(c *gin.Context) {
	userID, username, email := p.currentUser(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	filterType := c.Query("type")
	filterCategory := c.Query("category")

	query := `SELECT t.type, COALESCE(t.amount,0), t.category, COALESCE(t.description,''), t.date,
	                 COALESCE(g1.id, g2.id)::text, COALESCE(g1.name, g2.name, ''),
	                 COUNT(*) OVER() AS total
	          FROM transactions t
	          LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
	          LEFT JOIN groups g1 ON gt.group_id = g1.id
	          LEFT JOIN groups g2 ON t.group_id = g2.id
	          WHERE t.user_id = $1 AND t.is_deleted = FALSE`
	args := []interface{}{userID}
	n := 2

	if filterType != "" {
		query += fmt.Sprintf(" AND t.type = $%d", n)
		args = append(args, filterType)
		n++
	}
	if filterCategory != "" {
		query += fmt.Sprintf(" AND t.category = $%d", n)
		args = append(args, filterCategory)
		n++
	}
	query += fmt.Sprintf(" ORDER BY t.date DESC, t.created_at DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := p.pool.Query(c.Request.Context(), query, args...)
	var txs []portalTx
	total := 0
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tx portalTx
			var amount float64
			var date time.Time
			if rows.Scan(&tx.Type, &amount, &tx.Category, &tx.Description, &date, &tx.GroupID, &tx.GroupName, &total) == nil {
				tx.Amount = fmt.Sprintf("%.2f", amount)
				tx.Date = date.Format("2006-01-02")
				txs = append(txs, tx)
			}
		}
	}

	// Fetch distinct categories for filter dropdown
	catRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT DISTINCT category FROM transactions WHERE user_id=$1 AND is_deleted=FALSE ORDER BY category`, userID)
	var categories []string
	if catRows != nil {
		defer catRows.Close()
		for catRows.Next() {
			var cat string
			if catRows.Scan(&cat) == nil {
				categories = append(categories, cat)
			}
		}
	}

	totalPages := int(math.Ceil(float64(total) / float64(pageSize)))
	if totalPages < 1 {
		totalPages = 1
	}

	p.render(c, "transactions", gin.H{
		"Title":          "Transactions",
		"Active":         "transactions",
		"Username":       username,
		"Email":          email,
		"Transactions":   txs,
		"Total":          total,
		"Page":           page,
		"PrevPage":       page - 1,
		"NextPage":       page + 1,
		"TotalPages":     totalPages,
		"FilterType":     filterType,
		"FilterCategory": filterCategory,
		"Categories":     categories,
	})
}

func (p *Portal) transactionsExport(c *gin.Context) {
	// Proxy through to the existing CSV export endpoint using the same JWT.
	// Re-issue the JWT as a Bearer token, then redirect — or just serve the CSV directly.
	userID, _, _ := p.currentUser(c)

	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT t.type, t.amount, t.category, t.date, COALESCE(t.description,''), COALESCE(t.notes,''),
		        COALESCE(g1.name, g2.name, '')
		 FROM transactions t
		 LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
		 LEFT JOIN groups g1 ON gt.group_id = g1.id
		 LEFT JOIN groups g2 ON t.group_id = g2.id
		 WHERE t.user_id=$1 AND t.is_deleted=FALSE
		 ORDER BY t.date DESC, t.created_at DESC`, userID)
	if err != nil {
		c.String(500, "export failed")
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", `attachment; filename="transactions.csv"`)
	fmt.Fprint(c.Writer, "date,type,amount,category,description,notes,group_name\n")
	for rows.Next() {
		var txType, category, description, notes, groupName string
		var amount float64
		var date time.Time
		if rows.Scan(&txType, &amount, &category, &date, &description, &notes, &groupName) == nil {
			fmt.Fprintf(c.Writer, "%s,%s,%.2f,%s,%s,%s,%s\n",
				date.Format("2006-01-02"), txType, amount,
				csvEscape(category), csvEscape(description), csvEscape(notes), csvEscape(groupName))
		}
	}
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// ── Groups ────────────────────────────────────────────────────────────────────

type portalGroup struct {
	GroupID    string
	Name       string
	Members    []string
	TxCount    int
	AmountAbs  string
	IsPositive bool
	IsNegative bool
	CreatedAt  string
}

func (p *Portal) groupsPage(c *gin.Context) {
	userID, username, email := p.currentUser(c)

	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT g.id, g.name, g.created_at,
		        (SELECT COUNT(*) FROM group_transactions WHERE group_id=g.id AND is_deleted=FALSE)
		 FROM groups g
		 JOIN group_members gm ON gm.group_id=g.id
		 WHERE gm.user_id=$1 AND g.is_deleted=FALSE
		 ORDER BY g.created_at DESC`, userID)
	if err != nil {
		log.Printf("[PORTAL] groupsPage: %v", err)
		p.render(c, "groups", gin.H{"Title": "Groups", "Active": "groups", "Username": username, "Email": email})
		return
	}
	defer rows.Close()

	var groups []portalGroup
	for rows.Next() {
		var gid uuid.UUID
		var name string
		var createdAt time.Time
		var txCount int
		if rows.Scan(&gid, &name, &createdAt, &txCount) != nil {
			continue
		}

		// Fetch member usernames
		mRows, _ := p.pool.Query(c.Request.Context(),
			`SELECT u.username FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.group_id=$1 LIMIT 5`, gid)
		var members []string
		if mRows != nil {
			for mRows.Next() {
				var m string
				if mRows.Scan(&m) == nil {
					members = append(members, m)
				}
			}
			mRows.Close()
		}

		balance := p.computeGroupBalance(c, gid, userID)
		groups = append(groups, portalGroup{
			GroupID:    gid.String(),
			Name:       name,
			Members:    members,
			TxCount:    txCount,
			AmountAbs:  fmt.Sprintf("%.2f", math.Abs(balance)),
			IsPositive: balance > 0.005,
			IsNegative: balance < -0.005,
			CreatedAt:  createdAt.Format("Jan 2, 2006"),
		})
	}

	p.render(c, "groups", gin.H{
		"Title":    "Groups",
		"Active":   "groups",
		"Username": username,
		"Email":    email,
		"Groups":   groups,
	})
}

// ── Group Detail ──────────────────────────────────────────────────────────────

type groupDetailMember struct {
	Username      string
	Email         string
	IsCurrentUser bool
}

type groupDetailBalance struct {
	Username      string
	AmountAbs     string
	IsPositive    bool
	IsNegative    bool
	IsCurrentUser bool
}

type groupDetailTx struct {
	Date        string
	Description string
	Category    string
	PaidBy      string
	YourShare   string
	TotalAmount string
}

type groupDetailSettlement struct {
	Date     string
	FromUser string
	ToUser   string
	Amount   string
}

func (p *Portal) groupDetailPage(c *gin.Context) {
	userID, username, email := p.currentUser(c)

	gidStr := c.Param("id")
	gid, err := uuid.Parse(gidStr)
	if err != nil {
		c.Redirect(http.StatusFound, "/dashboard/groups")
		return
	}

	// Verify membership
	var isMember bool
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2)`, gid, userID).Scan(&isMember)
	if !isMember {
		c.Redirect(http.StatusFound, "/dashboard/groups")
		return
	}

	// Group info
	var groupName string
	var memberCount int
	var createdAt time.Time
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT name, (SELECT COUNT(*) FROM group_members WHERE group_id=$1), created_at
		 FROM groups WHERE id=$1 AND is_deleted=FALSE`, gid).Scan(&groupName, &memberCount, &createdAt)

	// Members
	mRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT u.id, u.username, u.email FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.group_id=$1 ORDER BY gm.joined_at`, gid)
	var members []groupDetailMember
	var memberIDs []uuid.UUID
	if mRows != nil {
		defer mRows.Close()
		for mRows.Next() {
			var mid uuid.UUID
			var m groupDetailMember
			if mRows.Scan(&mid, &m.Username, &m.Email) == nil {
				m.IsCurrentUser = mid == userID
				members = append(members, m)
				memberIDs = append(memberIDs, mid)
			}
		}
	}

	// Balances per member
	var balances []groupDetailBalance
	for i, mid := range memberIDs {
		bal := p.computeGroupBalance(c, gid, mid)
		balances = append(balances, groupDetailBalance{
			Username:      members[i].Username,
			AmountAbs:     fmt.Sprintf("%.2f", math.Abs(bal)),
			IsPositive:    bal > 0.005,
			IsNegative:    bal < -0.005,
			IsCurrentUser: mid == userID,
		})
	}

	// Transactions with user's split share
	txRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT gt.id, gt.category, COALESCE(gt.description,''), gt.total_amount, gt.date,
		        u.username,
		        COALESCE((SELECT amount FROM group_transaction_splits WHERE group_transaction_id=gt.id AND user_id=$2), 0)
		 FROM group_transactions gt
		 JOIN users u ON u.id=gt.paid_by_user_id
		 WHERE gt.group_id=$1 AND gt.is_deleted=FALSE
		 ORDER BY gt.date DESC LIMIT 30`, gid, userID)
	var transactions []groupDetailTx
	if txRows != nil {
		defer txRows.Close()
		for txRows.Next() {
			var gtid uuid.UUID
			var t groupDetailTx
			var total, yourShare float64
			var date time.Time
			if txRows.Scan(&gtid, &t.Category, &t.Description, &total, &date, &t.PaidBy, &yourShare) == nil {
				t.TotalAmount = fmt.Sprintf("%.2f", total)
				t.YourShare = fmt.Sprintf("%.2f", yourShare)
				t.Date = date.Format("Jan 2, 2006")
				transactions = append(transactions, t)
			}
		}
	}

	// Settlements
	sRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT fu.username, tu.username, s.amount, s.created_at
		 FROM settlements s
		 JOIN users fu ON fu.id=s.from_user
		 JOIN users tu ON tu.id=s.to_user
		 WHERE s.group_id=$1 AND s.is_deleted=FALSE
		 ORDER BY s.created_at DESC LIMIT 20`, gid)
	var settlements []groupDetailSettlement
	if sRows != nil {
		defer sRows.Close()
		for sRows.Next() {
			var s groupDetailSettlement
			var amount float64
			var date time.Time
			if sRows.Scan(&s.FromUser, &s.ToUser, &amount, &date) == nil {
				s.Amount = fmt.Sprintf("%.2f", amount)
				s.Date = date.Format("Jan 2, 2006")
				settlements = append(settlements, s)
			}
		}
	}

	p.render(c, "group_detail", gin.H{
		"Title":        groupName,
		"Active":       "groups",
		"Username":     username,
		"Email":        email,
		"GroupName":    groupName,
		"MemberCount":  memberCount,
		"CreatedAt":    createdAt.Format("Jan 2, 2006"),
		"Members":      members,
		"Balances":     balances,
		"Transactions": transactions,
		"Settlements":  settlements,
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// computeGroupBalance returns the current user's net balance in a group.
// Positive = others owe the user. Negative = user owes others.
func (p *Portal) computeGroupBalance(c *gin.Context, groupID, userID uuid.UUID) float64 {
	// Amount paid by user for the group
	var paid float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(total_amount),0) FROM group_transactions
		 WHERE group_id=$1 AND paid_by_user_id=$2 AND is_deleted=FALSE`,
		groupID, userID).Scan(&paid)

	// User's split owed
	var splitOwed float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(gts.amount),0)
		 FROM group_transaction_splits gts
		 JOIN group_transactions gt ON gts.group_transaction_id=gt.id
		 WHERE gt.group_id=$1 AND gts.user_id=$2 AND gt.is_deleted=FALSE`,
		groupID, userID).Scan(&splitOwed)

	// Settlements: from_user paid debt (+), to_user received (-)
	var settPaid, settReceived float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM settlements WHERE group_id=$1 AND from_user=$2 AND is_deleted=FALSE`,
		groupID, userID).Scan(&settPaid)
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM settlements WHERE group_id=$1 AND to_user=$2 AND is_deleted=FALSE`,
		groupID, userID).Scan(&settReceived)

	return paid - splitOwed + settPaid - settReceived
}

func (p *Portal) render(c *gin.Context, name string, data gin.H) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, ok := p.tmpls[name]
	if !ok {
		log.Printf("[PORTAL] unknown template %q", name)
		c.String(500, "Unknown template: %s", name)
		return
	}
	if err := tmpl.ExecuteTemplate(c.Writer, "layout", data); err != nil {
		log.Printf("[PORTAL] template error (%s): %v", name, err)
		c.String(500, "Template error: %v", err)
	}
}

func (p *Portal) renderLogin(c *gin.Context, errMsg string) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := p.tmpls["login"].Execute(c.Writer, gin.H{"Error": errMsg}); err != nil {
		log.Printf("[PORTAL] login template error: %v", err)
	}
}
