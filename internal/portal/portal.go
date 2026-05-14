package portal

import (
	"crypto/subtle"
	"fmt"
	"html/template"
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

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
	"github.com/yanonymousV2/finance-manager-backend/internal/category"
	"github.com/yanonymousV2/finance-manager-backend/internal/recurring"
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

	pages := []string{"dashboard", "transactions", "groups", "group_detail", "categories", "recurring", "budgets", "profile"}
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
		auth.GET("/categories", p.categoriesPage)
		auth.GET("/category-icons/:key", p.categoryIconServe)
		auth.GET("/recurring", p.recurringPage)
		auth.GET("/budgets", p.budgetsPage)
		auth.GET("/profile", p.profilePage)
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

type portalUser struct {
	ID             uuid.UUID
	Username       string
	Email          string
	CurrencySymbol string
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
		var u portalUser
		u.ID = userID
		var currency string
		_ = p.pool.QueryRow(c.Request.Context(),
			`SELECT username, email, currency FROM users WHERE id = $1`, u.ID,
		).Scan(&u.Username, &u.Email, &currency)
		u.CurrencySymbol = currencySymbol(currency)
		c.Set("portal_user", u)
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

func (p *Portal) currentUser(c *gin.Context) portalUser {
	u, _ := c.Get("portal_user")
	pu, _ := u.(portalUser)
	return pu
}

func currencySymbol(code string) string {
	switch code {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "JPY":
		return "¥"
	case "INR":
		return "₹"
	case "AUD":
		return "A$"
	case "CAD":
		return "C$"
	case "CHF":
		return "Fr"
	case "CNY":
		return "¥"
	case "SGD":
		return "S$"
	default:
		if code == "" {
			return "₹"
		}
		return code
	}
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
	u := p.currentUser(c)
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Total expenses this month
	var totalExpenses float64
	var expenseCount int
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0), COUNT(*) FROM transactions
		 WHERE user_id=$1 AND type='expense' AND date>=$2 AND date<$3 AND is_deleted=FALSE`,
		u.ID, monthStart, monthEnd).Scan(&totalExpenses, &expenseCount)

	// Total income this month
	var totalIncome float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM transactions
		 WHERE user_id=$1 AND type='income' AND date>=$2 AND date<$3 AND is_deleted=FALSE`,
		u.ID, monthStart, monthEnd).Scan(&totalIncome)

	// Budget
	var budgetLimit float64
	hasBudget := false
	var rawBudget *float64
	if err := p.pool.QueryRow(c.Request.Context(),
		`SELECT monthly_budget FROM users WHERE id=$1`, u.ID,
	).Scan(&rawBudget); err == nil && rawBudget != nil {
		budgetLimit = *rawBudget
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

	catNameMap := p.buildCategoryNameMap(c, u.ID)

	// Category breakdown
	catRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT category, COALESCE(SUM(amount),0), COUNT(*) FROM transactions
		 WHERE user_id=$1 AND type='expense' AND date>=$2 AND date<$3 AND is_deleted=FALSE
		 GROUP BY category ORDER BY SUM(amount) DESC LIMIT 8`,
		u.ID, monthStart, monthEnd)
	var categories []dashCategoryRow
	if catRows != nil {
		defer catRows.Close()
		for catRows.Next() {
			var r dashCategoryRow
			var total float64
			if catRows.Scan(&r.Category, &total, &r.ExpenseCount) == nil {
				r.Category = resolveDisplayName(catNameMap, r.Category)
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
		 ORDER BY g.created_at DESC`, u.ID)
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
			balance := p.computeGroupBalance(c, gid, u.ID)
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
		 ORDER BY date DESC, created_at DESC LIMIT 8`, u.ID)
	var recentTxs []dashRecentTx
	if txRows != nil {
		defer txRows.Close()
		for txRows.Next() {
			var r dashRecentTx
			var amount float64
			var date time.Time
			if txRows.Scan(&r.Type, &amount, &r.Category, &r.Description, &date) == nil {
				r.Category = resolveDisplayName(catNameMap, r.Category)
				r.Amount = fmt.Sprintf("%.2f", amount)
				r.Date = date.Format("Jan 2")
				recentTxs = append(recentTxs, r)
			}
		}
	}

	p.render(c, "dashboard", gin.H{
		"Title":              "Dashboard",
		"Active":             "dashboard",
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
	u := p.currentUser(c)
	catNameMap := p.buildCategoryNameMap(c, u.ID)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	filterType := c.Query("type")
	filterCategory := c.Query("category")

	query := `SELECT t.type, COALESCE(t.amount,0), t.category, COALESCE(t.description,''), t.date,
	                 COALESCE(g1.id::text, g2.id::text, ''), COALESCE(g1.name, g2.name, ''),
	                 COUNT(*) OVER() AS total
	          FROM transactions t
	          LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
	          LEFT JOIN groups g1 ON gt.group_id = g1.id
	          LEFT JOIN groups g2 ON t.group_id = g2.id
	          WHERE t.user_id = $1 AND t.is_deleted = FALSE`
	args := []interface{}{u.ID}
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
				tx.Category = resolveDisplayName(catNameMap, tx.Category)
				tx.Amount = fmt.Sprintf("%.2f", amount)
				tx.Date = date.Format("2006-01-02")
				txs = append(txs, tx)
			}
		}
	}

	// Fetch distinct categories for filter dropdown (resolve to display names)
	catRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT DISTINCT category FROM transactions WHERE user_id=$1 AND is_deleted=FALSE ORDER BY category`, u.ID)
	var categories []string
	if catRows != nil {
		defer catRows.Close()
		seen := make(map[string]bool)
		for catRows.Next() {
			var catKey string
			if catRows.Scan(&catKey) == nil {
				name := resolveDisplayName(catNameMap, catKey)
				if !seen[name] {
					seen[name] = true
					categories = append(categories, name)
				}
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
	u := p.currentUser(c)
	catNameMap := p.buildCategoryNameMap(c, u.ID)

	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT t.type, t.amount, t.category, t.date, COALESCE(t.description,''), COALESCE(t.notes,''),
		        COALESCE(g1.name, g2.name, '')
		 FROM transactions t
		 LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
		 LEFT JOIN groups g1 ON gt.group_id = g1.id
		 LEFT JOIN groups g2 ON t.group_id = g2.id
		 WHERE t.user_id=$1 AND t.is_deleted=FALSE
		 ORDER BY t.date DESC, t.created_at DESC`, u.ID)
	if err != nil {
		c.String(500, "export failed")
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", `attachment; filename="transactions.csv"`)
	_, _ = fmt.Fprint(c.Writer, "date,type,amount,category,description,notes,group_name\n")
	for rows.Next() {
		var txType, category, description, notes, groupName string
		var amount float64
		var date time.Time
		if rows.Scan(&txType, &amount, &category, &date, &description, &notes, &groupName) == nil {
			_, _ = fmt.Fprintf(c.Writer, "%s,%s,%.2f,%s,%s,%s,%s\n",
				date.Format("2006-01-02"), txType, amount,
				csvEscape(resolveDisplayName(catNameMap, category)), csvEscape(description), csvEscape(notes), csvEscape(groupName))
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
	u := p.currentUser(c)

	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT g.id, g.name, g.created_at,
		        (SELECT COUNT(*) FROM group_transactions WHERE group_id=g.id AND is_deleted=FALSE)
		 FROM groups g
		 JOIN group_members gm ON gm.group_id=g.id
		 WHERE gm.user_id=$1 AND g.is_deleted=FALSE
		 ORDER BY g.created_at DESC`, u.ID)
	if err != nil {
		applog.From(c).Error("portal: groupsPage failed", applog.KeyError, err)
		p.render(c, "groups", gin.H{"Title": "Groups", "Active": "groups"})
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

		balance := p.computeGroupBalance(c, gid, u.ID)
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
		"Title":  "Groups",
		"Active": "groups",
		"Groups": groups,
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
	u := p.currentUser(c)
	catNameMap := p.buildCategoryNameMap(c, u.ID)

	gidStr := c.Param("id")
	gid, err := uuid.Parse(gidStr)
	if err != nil {
		c.Redirect(http.StatusFound, "/dashboard/groups")
		return
	}

	// Verify membership
	var isMember bool
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2)`, gid, u.ID).Scan(&isMember)
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
				m.IsCurrentUser = mid == u.ID
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
			IsCurrentUser: mid == u.ID,
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
		 ORDER BY gt.date DESC LIMIT 30`, gid, u.ID)
	var transactions []groupDetailTx
	if txRows != nil {
		defer txRows.Close()
		for txRows.Next() {
			var gtid uuid.UUID
			var t groupDetailTx
			var total, yourShare float64
			var date time.Time
			if txRows.Scan(&gtid, &t.Category, &t.Description, &total, &date, &t.PaidBy, &yourShare) == nil {
				t.Category = resolveDisplayName(catNameMap, t.Category)
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
		"GroupName":    groupName,
		"MemberCount":  memberCount,
		"CreatedAt":    createdAt.Format("Jan 2, 2006"),
		"Members":      members,
		"Balances":     balances,
		"Transactions": transactions,
		"Settlements":  settlements,
	})
}

// ── Categories ────────────────────────────────────────────────────────────────

type portalCategory struct {
	Name         string
	Icon         string
	Color        string
	IsPredefined bool
	IsHidden     bool
	TxCount      int
	TotalSpent   string
}

func (p *Portal) categoryIconServe(c *gin.Context) {
	key := c.Param("key")
	data, ok := category.IconSVG(key)
	if !ok {
		c.Status(404)
		return
	}
	c.Data(200, "image/svg+xml", data)
}

func (p *Portal) categoriesPage(c *gin.Context) {
	u := p.currentUser(c)

	// Load visible predefined categories.
	predRows, err := p.pool.Query(c.Request.Context(),
		`SELECT key, name, icon, color FROM predefined_categories WHERE is_hidden = FALSE ORDER BY name ASC`)
	if err != nil {
		applog.From(c).Error("portal: categoriesPage predefined query failed", applog.KeyError, err)
		p.render(c, "categories", gin.H{"Title": "Categories", "Active": "categories"})
		return
	}
	defer predRows.Close()

	type predefinedEntry struct {
		Key, Name, Icon, Color string
	}
	var predefined []predefinedEntry
	for predRows.Next() {
		var e predefinedEntry
		if err := predRows.Scan(&e.Key, &e.Name, &e.Icon, &e.Color); err == nil {
			predefined = append(predefined, e)
		}
	}
	predRows.Close()

	// Load user's override and custom rows, with transaction stats.
	// Transactions now store category keys; join on the effective key for each row.
	userRows, err := p.pool.Query(c.Request.Context(),
		`SELECT cc.name, cc.icon, cc.color, cc.is_predefined, cc.is_hidden, cc.predefined_key,
		        COUNT(t.id) AS tx_count,
		        COALESCE(SUM(CASE WHEN t.type='expense' THEN t.amount ELSE 0 END), 0) AS total_spent
		 FROM custom_categories cc
		 LEFT JOIN transactions t ON t.user_id=cc.user_id AND t.is_deleted=FALSE
		   AND t.category = CASE WHEN cc.is_predefined THEN cc.predefined_key ELSE cc.key END
		 WHERE cc.user_id=$1
		 GROUP BY cc.name, cc.icon, cc.color, cc.is_predefined, cc.is_hidden, cc.predefined_key
		 ORDER BY cc.name ASC`, u.ID)
	if err != nil {
		applog.From(c).Error("portal: categoriesPage user query failed", applog.KeyError, err)
		p.render(c, "categories", gin.H{"Title": "Categories", "Active": "categories"})
		return
	}
	defer userRows.Close()

	overrides := make(map[string]portalCategory)
	var customCats []portalCategory
	for userRows.Next() {
		var r portalCategory
		var totalSpent float64
		var isPredefined bool
		var predKey *string
		if err := userRows.Scan(&r.Name, &r.Icon, &r.Color, &isPredefined, &r.IsHidden, &predKey, &r.TxCount, &totalSpent); err != nil {
			continue
		}
		r.IsPredefined = isPredefined
		if totalSpent > 0 {
			r.TotalSpent = fmt.Sprintf("%.2f", totalSpent)
		}
		if isPredefined && predKey != nil {
			overrides[*predKey] = r
		} else {
			customCats = append(customCats, r)
		}
	}

	// Get per-predefined-key transaction stats for entries that have no override row.
	type catStats struct {
		count int
		total float64
	}
	predStats := make(map[string]catStats)
	statsRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT category, COUNT(*), COALESCE(SUM(CASE WHEN type='expense' THEN amount ELSE 0 END),0)
		 FROM transactions WHERE user_id=$1 AND is_deleted=FALSE
		 GROUP BY category`, u.ID)
	if statsRows != nil {
		defer statsRows.Close()
		for statsRows.Next() {
			var k string
			var st catStats
			if statsRows.Scan(&k, &st.count, &st.total) == nil {
				predStats[k] = st
			}
		}
	}

	// Merge: predefined (with overrides applied) then custom.
	var categories []portalCategory
	for _, p := range predefined {
		if ov, ok := overrides[p.Key]; ok {
			categories = append(categories, ov)
		} else {
			cat := portalCategory{
				Name:         p.Name,
				Icon:         p.Icon,
				Color:        p.Color,
				IsPredefined: true,
			}
			if st, ok := predStats[p.Key]; ok {
				cat.TxCount = st.count
				if st.total > 0 {
					cat.TotalSpent = fmt.Sprintf("%.2f", st.total)
				}
			}
			categories = append(categories, cat)
		}
	}
	categories = append(categories, customCats...)

	customCount := 0
	for _, cat := range categories {
		if !cat.IsPredefined {
			customCount++
		}
	}

	p.render(c, "categories", gin.H{
		"Title":       "Categories",
		"Active":      "categories",
		"Categories":  categories,
		"Total":       len(categories),
		"CustomCount": customCount,
	})
}

// ── Recurring ─────────────────────────────────────────────────────────────────

type portalRecurring struct {
	Name      string
	Type      string
	Category  string
	Frequency string
	Amount    string
	Notes     string
	IsActive  bool
	NextDate  string
}

func (p *Portal) recurringPage(c *gin.Context) {
	u := p.currentUser(c)
	catNameMap := p.buildCategoryNameMap(c, u.ID)

	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT name, type, category, frequency, amount, COALESCE(notes,''), is_active,
		        last_added_date, start_date, end_date, day_of_month, days_of_week
		 FROM recurring_transactions WHERE user_id=$1 ORDER BY is_active DESC, name ASC`, u.ID)
	if err != nil {
		applog.From(c).Error("portal: recurringPage failed", applog.KeyError, err)
		p.render(c, "recurring", gin.H{"Title": "Recurring", "Active": "recurring"})
		return
	}
	defer rows.Close()

	var recs []portalRecurring
	activeCount := 0
	for rows.Next() {
		var r portalRecurring
		var amount float64
		var lastAdded *time.Time
		var startDate time.Time
		var endDate *time.Time
		var dayOfMonth *int
		var daysOfWeekRaw []int32
		if rows.Scan(&r.Name, &r.Type, &r.Category, &r.Frequency, &amount, &r.Notes, &r.IsActive, &lastAdded, &startDate, &endDate, &dayOfMonth, &daysOfWeekRaw) == nil {
			r.Category = resolveDisplayName(catNameMap, r.Category)
			r.Amount = fmt.Sprintf("%.2f", amount)
			if r.IsActive {
				activeCount++
				base := startDate
				if lastAdded != nil {
					base = *lastAdded
				}
				dow := make([]int, len(daysOfWeekRaw))
				for i, v := range daysOfWeekRaw {
					dow[i] = int(v)
				}
				today := time.Now().UTC().Truncate(24 * time.Hour)
				next := recurring.NextFutureOccurrence(base, r.Frequency, dayOfMonth, dow, today, time.UTC)
				if next != nil && (endDate == nil || next.Before(*endDate)) {
					r.NextDate = next.Format("Jan 2, 2006")
				}
			}
			recs = append(recs, r)
		}
	}

	p.render(c, "recurring", gin.H{
		"Title":       "Recurring",
		"Active":      "recurring",
		"Recurring":   recs,
		"Total":       len(recs),
		"ActiveCount": activeCount,
	})
}

// ── Budgets ───────────────────────────────────────────────────────────────────

type portalBudgetRow struct {
	MonthName   string
	Year        int
	BudgetLimit string
	Spent       string
	Remaining   string
	Pct         int
	IsOver      bool
}

func (p *Portal) budgetsPage(c *gin.Context) {
	u := p.currentUser(c)

	var rawBudget *float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT monthly_budget FROM users WHERE id=$1`, u.ID,
	).Scan(&rawBudget)

	if rawBudget == nil {
		p.render(c, "budgets", gin.H{
			"Title":          "Budgets",
			"Active":         "budgets",
			"CurrencySymbol": u.CurrencySymbol,
		})
		return
	}
	limit := *rawBudget

	now := time.Now().UTC()
	var rows []portalBudgetRow
	for i := 0; i < 6; i++ {
		t := now.AddDate(0, -i, 0)
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)

		var spent float64
		_ = p.pool.QueryRow(c.Request.Context(),
			`SELECT COALESCE(SUM(amount),0) FROM transactions
			 WHERE user_id=$1 AND type='expense' AND date>=$2 AND date<$3 AND is_deleted=FALSE`,
			u.ID, start, end,
		).Scan(&spent)

		remaining := limit - spent
		isOver := spent > limit
		pct := 0
		if limit > 0 {
			p := (spent / limit) * 100
			if p > 100 {
				p = 100
			}
			pct = int(p)
		}
		rows = append(rows, portalBudgetRow{
			MonthName:   t.Month().String(),
			Year:        t.Year(),
			BudgetLimit: fmt.Sprintf("%.2f", limit),
			Spent:       fmt.Sprintf("%.2f", spent),
			Remaining:   fmt.Sprintf("%.2f", remaining),
			Pct:         pct,
			IsOver:      isOver,
		})
	}

	p.render(c, "budgets", gin.H{
		"Title":          "Budgets",
		"Active":         "budgets",
		"CurrencySymbol": u.CurrencySymbol,
		"Budgets":        rows,
	})
}

// ── Profile ───────────────────────────────────────────────────────────────────

type profileTopCategory struct {
	Category   string
	TxCount    int
	TotalSpent string
}

func (p *Portal) profilePage(c *gin.Context) {
	u := p.currentUser(c)
	catNameMap := p.buildCategoryNameMap(c, u.ID)

	// Account creation date
	var createdAt time.Time
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT created_at FROM users WHERE id=$1`, u.ID).Scan(&createdAt)

	accountAge := formatDuration(time.Since(createdAt))

	// Lifetime stats
	var totalTx int
	var totalExpenses, totalIncome float64
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN type='expense' THEN amount ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN type='income' THEN amount ELSE 0 END),0)
		 FROM transactions WHERE user_id=$1 AND is_deleted=FALSE`, u.ID,
	).Scan(&totalTx, &totalExpenses, &totalIncome)

	var groupCount int
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM group_members gm JOIN groups g ON g.id=gm.group_id WHERE gm.user_id=$1 AND g.is_deleted=FALSE`, u.ID,
	).Scan(&groupCount)

	var recurringCount int
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM recurring_transactions WHERE user_id=$1 AND is_active=TRUE`, u.ID,
	).Scan(&recurringCount)

	var customCategoryCount int
	_ = p.pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM custom_categories WHERE user_id=$1 AND is_predefined=FALSE`, u.ID,
	).Scan(&customCategoryCount)

	// All-time top categories
	catRows, _ := p.pool.Query(c.Request.Context(),
		`SELECT category, COUNT(*), COALESCE(SUM(amount),0)
		 FROM transactions WHERE user_id=$1 AND type='expense' AND is_deleted=FALSE
		 GROUP BY category ORDER BY SUM(amount) DESC LIMIT 10`, u.ID)
	var topCategories []profileTopCategory
	if catRows != nil {
		defer catRows.Close()
		for catRows.Next() {
			var r profileTopCategory
			var spent float64
			if catRows.Scan(&r.Category, &r.TxCount, &spent) == nil {
				r.Category = resolveDisplayName(catNameMap, r.Category)
				r.TotalSpent = fmt.Sprintf("%.2f", spent)
				topCategories = append(topCategories, r)
			}
		}
	}

	p.render(c, "profile", gin.H{
		"Title":               "Profile",
		"Active":              "profile",
		"UserID":              u.ID.String(),
		"MemberSince":         createdAt.Format("January 2, 2006"),
		"AccountAge":          accountAge,
		"TotalTx":             totalTx,
		"TotalExpenses":       fmt.Sprintf("%.2f", totalExpenses),
		"TotalIncome":         fmt.Sprintf("%.2f", totalIncome),
		"GroupCount":          groupCount,
		"RecurringCount":      recurringCount,
		"CustomCategoryCount": customCategoryCount,
		"TopCategories":       topCategories,
	})
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days < 1 {
		return "less than a day"
	}
	if days < 30 {
		return fmt.Sprintf("%d days", days)
	}
	months := days / 30
	if months < 12 {
		return fmt.Sprintf("%d months", months)
	}
	years := months / 12
	rem := months % 12
	if rem == 0 {
		return fmt.Sprintf("%d years", years)
	}
	return fmt.Sprintf("%d years, %d months", years, rem)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildCategoryNameMap returns a map from category key → display name for the
// given user. Predefined categories come first; user overrides and custom
// categories overwrite the predefined entry for the same key.
func (p *Portal) buildCategoryNameMap(c *gin.Context, userID uuid.UUID) map[string]string {
	m := make(map[string]string)

	// Load all predefined categories.
	rows, err := p.pool.Query(c.Request.Context(),
		`SELECT key, name FROM predefined_categories WHERE is_hidden = FALSE`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, name string
			if rows.Scan(&k, &name) == nil {
				m[k] = name
			}
		}
	}

	// User overrides: for predefined overrides key is predefined_key; for custom
	// rows key is the stable key column.
	uRows, err := p.pool.Query(c.Request.Context(),
		`SELECT CASE WHEN is_predefined THEN predefined_key ELSE key END AS cat_key, name
		 FROM custom_categories
		 WHERE user_id = $1 AND is_hidden = FALSE`, userID)
	if err == nil {
		defer uRows.Close()
		for uRows.Next() {
			var k, name string
			if uRows.Scan(&k, &name) == nil && k != "" {
				m[k] = name
			}
		}
	}

	return m
}

// resolveDisplayName returns the display name for a category key. Falls back
// to "Other" when the key is unknown.
func resolveDisplayName(m map[string]string, key string) string {
	if name, ok := m[key]; ok {
		return name
	}
	if name, ok := m["other"]; ok {
		return name
	}
	return "Other"
}

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
		applog.From(c).Error("portal: unknown template", "template", name)
		c.String(500, "Unknown template: %s", name)
		return
	}
	u := p.currentUser(c)
	data["Username"] = u.Username
	data["Email"] = u.Email
	data["CurrencySymbol"] = u.CurrencySymbol
	if err := tmpl.ExecuteTemplate(c.Writer, "layout", data); err != nil {
		applog.From(c).Error("portal: template execute failed", "template", name, applog.KeyError, err)
		c.String(500, "Template error: %v", err)
	}
}

func (p *Portal) renderLogin(c *gin.Context, errMsg string) {
	c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := p.tmpls["login"].Execute(c.Writer, gin.H{"Error": errMsg}); err != nil {
		applog.From(c).Error("portal: login template execute failed", applog.KeyError, err)
	}
}
