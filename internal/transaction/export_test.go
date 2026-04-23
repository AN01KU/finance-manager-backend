package transaction

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatCSVRow(t *testing.T) {
	str := func(s string) *string { return &s }

	tests := []struct {
		name string
		row  csvRow
		want string
	}{
		{
			name: "basic expense row",
			row: csvRow{
				Date:        time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
				Type:        "expense",
				Amount:      "42.50",
				Category:    "Dining",
				Description: str("Dinner with friends"),
				Notes:       nil,
				GroupName:   nil,
			},
			want: "2026-04-15,expense,42.50,Dining,Dinner with friends,,\n",
		},
		{
			name: "income row with no description",
			row: csvRow{
				Date:        time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
				Type:        "income",
				Amount:      "5000.00",
				Category:    "Salary",
				Description: nil,
				Notes:       nil,
				GroupName:   nil,
			},
			want: "2026-03-01,income,5000.00,Salary,,,\n",
		},
		{
			name: "group expense with notes",
			row: csvRow{
				Date:        time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
				Type:        "expense",
				Amount:      "25.00",
				Category:    "Transport",
				Description: str("Uber to airport"),
				Notes:       str("shared with Alice"),
				GroupName:   str("Trip Squad"),
			},
			want: "2026-04-10,expense,25.00,Transport,Uber to airport,shared with Alice,Trip Squad\n",
		},
		{
			name: "description with comma is quoted",
			row: csvRow{
				Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
				Type:        "expense",
				Amount:      "10.00",
				Category:    "Groceries",
				Description: str("milk, eggs, bread"),
				Notes:       nil,
				GroupName:   nil,
			},
			want: "2026-04-01,expense,10.00,Groceries,\"milk, eggs, bread\",,\n",
		},
		{
			name: "description with double quote is escaped",
			row: csvRow{
				Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
				Type:        "expense",
				Amount:      "10.00",
				Category:    "Other",
				Description: str(`said "hello"`),
				Notes:       nil,
				GroupName:   nil,
			},
			want: "2026-04-01,expense,10.00,Other,\"said \"\"hello\"\"\",,\n",
		},
		{
			name: "group name with comma is quoted",
			row: csvRow{
				Date:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
				Type:        "expense",
				Amount:      "50.00",
				Category:    "Dining",
				Description: nil,
				Notes:       nil,
				GroupName:   str("Alice, Bob"),
			},
			want: "2026-04-01,expense,50.00,Dining,,,\"Alice, Bob\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := writeCSVRow(&buf, tt.row)
			require.NoError(t, err)
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestCSVHeader(t *testing.T) {
	var buf bytes.Buffer
	writeCSVHeader(&buf)
	assert.Equal(t, "date,type,amount,category,description,notes,group_name\n", buf.String())
}
