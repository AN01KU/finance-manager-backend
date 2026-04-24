package transaction

import (
	"encoding/csv"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
	"github.com/yanonymousV2/finance-manager-backend/internal/middleware"
)

// csvRow holds one transaction's data for CSV output.
type csvRow struct {
	Date        time.Time
	Type        string
	Amount      string
	Category    string
	Description *string
	Notes       *string
	GroupName   *string
}

// writeCSVHeader writes the header line to w.
func writeCSVHeader(w io.Writer) {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "type", "amount", "category", "description", "notes", "group_name"})
	cw.Flush()
}

// writeCSVRow writes a single data row to w using standard CSV encoding.
func writeCSVRow(w io.Writer, row csvRow) error {
	cw := csv.NewWriter(w)
	record := []string{
		row.Date.Format("2006-01-02"),
		row.Type,
		row.Amount,
		row.Category,
		derefStr(row.Description),
		derefStr(row.Notes),
		derefStr(row.GroupName),
	}
	if err := cw.Write(record); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ExportTransactionsCSV streams the authenticated user's transactions as a CSV file.
// Supports the same filters as ListTransactions: type, category, start_date, end_date.
func ExportTransactionsCSV(c *gin.Context, database *db.DB) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	query := `SELECT t.type, t.amount, t.category, t.date, t.description, t.notes,
	                 COALESCE(g1.name, g2.name) AS group_name
	          FROM transactions t
	          LEFT JOIN group_transactions gt ON t.group_transaction_id = gt.id
	          LEFT JOIN groups g1 ON gt.group_id = g1.id
	          LEFT JOIN groups g2 ON t.group_id = g2.id
	          WHERE t.user_id = $1 AND t.is_deleted = FALSE`
	args := []interface{}{userID}
	n := 2

	if v := c.Query("type"); v != "" {
		query += " AND t.type = $" + strconv.Itoa(n)
		args = append(args, v)
		n++
	}
	if v := c.Query("category"); v != "" {
		query += " AND t.category = $" + strconv.Itoa(n)
		args = append(args, v)
		n++
	}
	if v := c.Query("start_date"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			query += " AND t.date >= $" + strconv.Itoa(n)
			args = append(args, time.UnixMilli(ms).UTC())
			n++
		}
	}
	if v := c.Query("end_date"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			query += " AND t.date <= $" + strconv.Itoa(n)
			args = append(args, time.UnixMilli(ms).UTC())
			n++
		}
	}

	query += " ORDER BY t.date DESC, t.created_at DESC"

	rows, err := database.Pool.Query(c.Request.Context(), query, args...)
	if err != nil {
		log.Printf("[ERROR] ExportTransactionsCSV query: %v", err)
		c.JSON(500, gin.H{"error": "failed to export transactions"})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=\"transactions.csv\"")

	writeCSVHeader(c.Writer)

	for rows.Next() {
		var txType, category string
		var amount decimal.Decimal
		var date time.Time
		var description, notes, groupName *string

		if err := rows.Scan(&txType, &amount, &category, &date, &description, &notes, &groupName); err != nil {
			log.Printf("[ERROR] ExportTransactionsCSV scan: %v", err)
			return
		}

		if err := writeCSVRow(c.Writer, csvRow{
			Date:        date,
			Type:        txType,
			Amount:      amount.StringFixed(2),
			Category:    category,
			Description: description,
			Notes:       notes,
			GroupName:   groupName,
		}); err != nil {
			log.Printf("[ERROR] ExportTransactionsCSV write: %v", err)
			return
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("[ERROR] ExportTransactionsCSV rows: %v", err)
	}
}
