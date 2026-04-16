package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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

	pages := []string{"dashboard", "tables", "table_browse", "row_form", "sql", "logs"}
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
		protected.GET("/sql", a.sqlPage)
		protected.POST("/sql", a.sqlExec)
		protected.POST("/rows/delete", a.rowDelete)
		protected.GET("/logs", a.logsPage)
		protected.POST("/logs/clear", a.logsClear)
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
	_ = a.pool.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM users`).Scan(&userCount)

	var dbSize string
	_ = a.pool.QueryRow(c.Request.Context(),
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
