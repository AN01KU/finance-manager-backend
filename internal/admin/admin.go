package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
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
		if statusFilter != "" && e.StatusClass != statusFilter {
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
		ls.Add(LogEntry{
			Time:        start.Format("15:04:05"),
			Method:      c.Request.Method,
			Path:        c.Request.URL.Path,
			Status:      status,
			StatusClass: statusClass,
			Duration:    time.Since(start).Round(time.Microsecond).String(),
		})
	}
}

// Admin holds all dependencies for the admin dashboard.
type Admin struct {
	pool      *pgxpool.Pool
	tmpls     map[string]*template.Template
	username  string
	password  string
	sessions  map[string]time.Time
	mu        sync.Mutex
	logStore  *LogStore
}

// New creates a new Admin instance.
func New(pool *pgxpool.Pool, username, password string, logStore *LogStore) *Admin {
	dir := filepath.Join("internal", "admin", "templates")
	layoutFile := filepath.Join(dir, "layout.html")

	pages := []string{"dashboard", "tables", "table_browse", "sql", "logs"}
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
func (a *Admin) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/admin")

	// Public routes
	g.GET("/login", a.loginPage)
	g.POST("/login", a.loginSubmit)

	// Protected routes
	protected := g.Group("/")
	protected.Use(a.authMiddleware())
	{
		protected.GET("/", a.dashboard)
		protected.GET("/tables", a.tables)
		protected.GET("/tables/:name", a.tableBrowse)
		protected.GET("/sql", a.sqlPage)
		protected.POST("/sql", a.sqlExec)
		protected.POST("/rows/delete", a.rowDelete)
		protected.GET("/logs", a.logsPage)
		protected.GET("/logs/clear", a.logsClear)
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
	a.tmpls["login"].ExecuteTemplate(c.Writer, "login.html", gin.H{"Error": ""})
}

func (a *Admin) loginSubmit(c *gin.Context) {
	user := c.PostForm("username")
	pass := c.PostForm("password")
	if user == a.username && pass == a.password && a.password != "" {
		token := a.createSession()
		c.SetCookie(sessionCookieName, token, sessionMaxAge, "/admin", "", false, true)
		c.Redirect(http.StatusFound, "/admin/")
		return
	}
	a.tmpls["login"].ExecuteTemplate(c.Writer, "login.html", gin.H{"Error": "Invalid credentials"})
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
	Tables    int
	TotalRows int64
	Users     int64
	DBSize    string
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
	a.pool.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM users`).Scan(&userCount)

	var dbSize string
	a.pool.QueryRow(c.Request.Context(),
		`SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&dbSize)

	a.render(c, "dashboard.html", gin.H{
		"Title":  "Dashboard",
		"Active": "dashboard",
		"Tables": tables,
		"Stats":  dashStats{Tables: len(tables), TotalRows: totalRows, Users: userCount, DBSize: dbSize},
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
	a.pool.QueryRow(c.Request.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM %q`, tableName)).Scan(&totalRows)

	// Get primary key column
	var pkColumn string
	a.pool.QueryRow(c.Request.Context(),
		`SELECT a.attname FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = $1::regclass AND i.indisprimary LIMIT 1`, tableName).Scan(&pkColumn)
	if pkColumn == "" {
		pkColumn = "id"
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
				row[i] = fmt.Sprintf("%v", v)
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
		"PKColumn":  pkColumn,
		"TotalRows": totalRows,
		"Page":      page,
		"PrevPage":  page - 1,
		"NextPage":  page + 1,
		"HasNext":   int64(offset+pageSize) < totalRows,
	})
}

// --- Row Delete ---

func (a *Admin) rowDelete(c *gin.Context) {
	table := c.PostForm("table")
	pkCol := c.PostForm("pk_column")
	pkVal := c.PostForm("pk_value")

	// Validate table exists
	var exists bool
	a.pool.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename=$1)`, table).Scan(&exists)
	if !exists {
		c.Redirect(http.StatusFound, "/admin/tables")
		return
	}

	query := fmt.Sprintf(`DELETE FROM %q WHERE %q = $1`, table, pkCol)
	_, err := a.pool.Exec(c.Request.Context(), query, pkVal)
	if err != nil {
		log.Printf("admin: delete error: %v", err)
	}
	c.Redirect(http.StatusFound, "/admin/tables/"+table)
}

// --- SQL Runner ---

func (a *Admin) sqlPage(c *gin.Context) {
	a.render(c, "sql.html", gin.H{
		"Title":  "SQL Runner",
		"Active": "sql",
	})
}

func (a *Admin) sqlExec(c *gin.Context) {
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
					row[i] = fmt.Sprintf("%v", v)
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
