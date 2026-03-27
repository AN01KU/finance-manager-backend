package seed

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/db"
)

// Fixed UUIDs for stable references across restarts
var (
	// Users
	UserAnkushID = uuid.MustParse("aaaaaaaa-0001-0001-0001-aaaaaaaaaaaa")
	UserPriyaID  = uuid.MustParse("bbbbbbbb-0002-0002-0002-bbbbbbbbbbbb")
	UserRahulID  = uuid.MustParse("cccccccc-0003-0003-0003-cccccccccccc")
	UserSaraID   = uuid.MustParse("dddddddd-0004-0004-0004-dddddddddddd")

	// Groups
	GroupRoommatesID = uuid.MustParse("eeeeeeee-0001-0001-0001-eeeeeeeeeeee")
	GroupTripGoaID   = uuid.MustParse("ffffffff-0002-0002-0002-ffffffffffff")
	GroupOldTripID   = uuid.MustParse("11111111-0003-0003-0003-111111111111") // group Ankush is NOT a member of

	// Recurring expenses (Ankush)
	RecurringRentID     = uuid.MustParse("22222222-0001-0001-0001-222222222222")
	RecurringNetflixID  = uuid.MustParse("33333333-0002-0002-0002-333333333333")
	RecurringGymID      = uuid.MustParse("44444444-0003-0003-0003-444444444444")
	RecurringInactiveID = uuid.MustParse("55555555-0004-0004-0004-555555555555")
)

var seededUserIDs = []uuid.UUID{UserAnkushID, UserPriyaID, UserRahulID, UserSaraID}

// Seed inserts all test data into the database.
// It is idempotent — safe to call on every startup.
func Seed(ctx context.Context, database *db.DB) error {
	log.Println("[seed] Seeding test data...")

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	twoDaysAgo := today.AddDate(0, 0, -2)
	threeDaysAgo := today.AddDate(0, 0, -3)
	lastWeek := today.AddDate(0, 0, -7)
	lastMonth := today.AddDate(0, -1, 0)

	// ── Users ──────────────────────────────────────────────────────────────────
	users := []struct {
		id       uuid.UUID
		email    string
		username string
		password string
	}{
		{UserAnkushID, "ankush@gmail.com", "Ankush", "12345678"},
		{UserPriyaID, "priya@gmail.com", "Priya", "12345678"},
		{UserRahulID, "rahul@gmail.com", "Rahul", "12345678"},
		{UserSaraID, "sara@gmail.com", "Sara", "12345678"},
	}

	for _, u := range users {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("bcrypt error: %w", err)
		}
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO users (id, email, username, password_hash)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (id) DO NOTHING`,
			u.id, u.email, u.username, string(hash))
		if err != nil {
			return fmt.Errorf("insert user %s: %w", u.email, err)
		}
	}
	log.Println("[seed] ✓ Users")

	// ── Predefined categories for all users ───────────────────────────────────
	type predefinedCat struct {
		Key   string
		Name  string
		Icon  string
		Color string
	}
	predefined := []predefinedCat{
		{"foodDining", "Food & Dining", "fork.knife.circle.fill", "#FF6B6B"},
		{"transport", "Transport", "car.circle.fill", "#4ECDC4"},
		{"housing", "Housing", "house.circle.fill", "#45B7D1"},
		{"healthMedical", "Health & Medical", "cross.case.circle.fill", "#96CEB4"},
		{"shopping", "Shopping", "bag.circle.fill", "#FFEAA7"},
		{"utilities", "Utilities", "bolt.square.fill", "#DDA15E"},
		{"entertainment", "Entertainment", "gamecontroller.circle.fill", "#BC6C25"},
		{"travel", "Travel", "airplane.circle.fill", "#8E44AD"},
		{"workProfessional", "Work & Professional", "briefcase.circle.fill", "#34495E"},
		{"education", "Education", "book.circle.fill", "#3498DB"},
		{"debtPayments", "Debt & Payments", "creditcard.circle.fill", "#2C3E50"},
		{"booksMedia", "Books & Media", "book.closed.circle.fill", "#E74C3C"},
		{"familyKids", "Family & Kids", "figure.2.and.child.holdinghands", "#F39C12"},
		{"gifts", "Gifts", "gift.circle.fill", "#E91E63"},
		{"other", "Other", "ellipsis.circle.fill", "#95A5A6"},
	}

	for _, uid := range seededUserIDs {
		for _, cat := range predefined {
			_, err := database.Pool.Exec(ctx,
				`INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
				 VALUES ($1, $2, $3, $4, false, true, $5)
				 ON CONFLICT DO NOTHING`,
				uid, cat.Name, cat.Icon, cat.Color, cat.Key)
			if err != nil {
				return fmt.Errorf("seed predefined category %s for user %s: %w", cat.Key, uid, err)
			}
		}
	}

	// Ankush: hide Shopping category
	if _, err := database.Pool.Exec(ctx,
		`UPDATE custom_categories SET is_hidden = true WHERE user_id = $1 AND predefined_key = 'shopping'`,
		UserAnkushID); err != nil {
		return fmt.Errorf("hide shopping category: %w", err)
	}

	// Ankush: two custom categories
	for _, cc := range []struct{ name, icon, color string }{
		{"Subscriptions", "antenna.radiowaves.left.and.right.circle.fill", "#6C5CE7"},
		{"Gym & Fitness", "figure.run.circle.fill", "#00B894"},
	} {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
			 VALUES ($1, $2, $3, $4, false, false, NULL)
			 ON CONFLICT (user_id, name) DO NOTHING`,
			UserAnkushID, cc.name, cc.icon, cc.color); err != nil {
			return fmt.Errorf("insert custom category %s: %w", cc.name, err)
		}
	}
	log.Println("[seed] ✓ Categories")

	// ── Budgets (Ankush) ───────────────────────────────────────────────────────
	prevMonth := int(now.Month()) - 1
	prevYear := now.Year()
	if prevMonth == 0 {
		prevMonth = 12
		prevYear--
	}

	for _, b := range []struct {
		year, month int
		limit       float64
	}{
		{now.Year(), int(now.Month()), 30000.00},
		{prevYear, prevMonth, 25000.00},
	} {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO monthly_budgets (user_id, year, month, budget_limit)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id, year, month) DO NOTHING`,
			UserAnkushID, b.year, b.month, b.limit); err != nil {
			return fmt.Errorf("insert budget: %w", err)
		}
	}
	log.Println("[seed] ✓ Budgets")

	// ── Recurring expenses (Ankush) ────────────────────────────────────────────
	recs := []struct {
		id         uuid.UUID
		name       string
		amount     float64
		category   string
		frequency  string
		dayOfMonth *int
		daysOfWeek []int
		startDate  time.Time
		endDate    *time.Time
		isActive   bool
		lastAdded  *time.Time
		notes      string
	}{
		{RecurringRentID, "House Rent", 15000.00, "Housing", "monthly", intPtr(1), nil, lastMonth, nil, true, &today, "Monthly rent for flat"},
		{RecurringNetflixID, "Netflix", 649.00, "Subscriptions", "monthly", intPtr(15), nil, lastMonth, nil, true, &lastMonth, "Streaming subscription"},
		{RecurringGymID, "Gym Membership", 1200.00, "Gym & Fitness", "monthly", intPtr(5), nil, lastMonth, nil, true, &today, "Monthly gym fee"},
		{RecurringInactiveID, "Spotify", 119.00, "Subscriptions", "monthly", intPtr(20), nil, lastMonth.AddDate(-1, 0, 0), &lastMonth, false, &lastMonth, "Cancelled — switched to YouTube Music"},
	}

	for _, r := range recs {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO recurring_expenses
			 (id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			 ON CONFLICT (id) DO NOTHING`,
			r.id, UserAnkushID, r.name, r.amount, r.category, r.frequency,
			r.dayOfMonth, r.daysOfWeek, r.startDate, r.endDate, r.isActive, r.lastAdded, r.notes); err != nil {
			return fmt.Errorf("insert recurring %s: %w", r.name, err)
		}
	}
	log.Println("[seed] ✓ Recurring expenses")

	// ── Personal transactions (Ankush) ─────────────────────────────────────────
	// type: 'expense' or 'income'
	type txRow struct {
		id                 uuid.UUID
		txType             string
		amount             float64
		category           string
		date               time.Time
		description        string
		notes              string
		isDeleted          bool
		recurringExpID     *uuid.UUID
	}

	personalTxs := []txRow{
		// Today — expenses
		{uuid.MustParse("a1000001-0000-0000-0000-000000000000"), "expense", 320.00, "Food & Dining", today, "Lunch - Subway", "", false, nil},
		{uuid.MustParse("a1000002-0000-0000-0000-000000000000"), "expense", 45.00, "Transport", today, "Auto to office", "", false, nil},
		{uuid.MustParse("a1000003-0000-0000-0000-000000000000"), "expense", 15000.00, "Housing", today, "House Rent - March", "Paid via UPI", false, &RecurringRentID},
		{uuid.MustParse("a1000004-0000-0000-0000-000000000000"), "expense", 1200.00, "Gym & Fitness", today, "Gym Membership March", "", false, &RecurringGymID},
		// Today — income (salary)
		{uuid.MustParse("a1000005-0000-0000-0000-000000000000"), "income", 85000.00, "Work & Professional", today, "March Salary", "Net after TDS", false, nil},

		// Yesterday
		{uuid.MustParse("a2000001-0000-0000-0000-000000000000"), "expense", 580.00, "Food & Dining", yesterday, "Dinner - Barbeque Nation", "Split with friends", false, nil},
		{uuid.MustParse("a2000002-0000-0000-0000-000000000000"), "expense", 199.00, "Shopping", yesterday, "T-shirt from Myntra", "", false, nil},
		{uuid.MustParse("a2000003-0000-0000-0000-000000000000"), "expense", 60.00, "Transport", yesterday, "Cab to mall", "", false, nil},
		{uuid.MustParse("a2000004-0000-0000-0000-000000000000"), "expense", 250.00, "Health & Medical", yesterday, "Pharmacy - vitamins", "", false, nil},

		// 2 days ago
		{uuid.MustParse("a3000001-0000-0000-0000-000000000000"), "expense", 120.00, "Food & Dining", twoDaysAgo, "Coffee + snacks", "", false, nil},
		{uuid.MustParse("a3000002-0000-0000-0000-000000000000"), "expense", 1499.00, "Entertainment", twoDaysAgo, "Movie tickets (2x)", "", false, nil},
		{uuid.MustParse("a3000003-0000-0000-0000-000000000000"), "expense", 649.00, "Subscriptions", twoDaysAgo, "Netflix - March", "", false, &RecurringNetflixID},

		// 3 days ago
		{uuid.MustParse("a4000001-0000-0000-0000-000000000000"), "expense", 85.00, "Transport", threeDaysAgo, "Metro card recharge", "", false, nil},
		{uuid.MustParse("a4000002-0000-0000-0000-000000000000"), "expense", 450.00, "Food & Dining", threeDaysAgo, "Grocery shopping", "", false, nil},
		{uuid.MustParse("a4000003-0000-0000-0000-000000000000"), "expense", 2000.00, "Education", threeDaysAgo, "Udemy course", "Go programming advanced", false, nil},

		// Last week
		{uuid.MustParse("a5000001-0000-0000-0000-000000000000"), "expense", 3200.00, "Shopping", lastWeek, "Nike shoes", "", false, nil},
		{uuid.MustParse("a5000002-0000-0000-0000-000000000000"), "expense", 780.00, "Food & Dining", lastWeek, "Team lunch at office", "", false, nil},
		{uuid.MustParse("a5000003-0000-0000-0000-000000000000"), "expense", 400.00, "Utilities", lastWeek, "Electricity bill", "", false, nil},
		// Last week — freelance income
		{uuid.MustParse("a5000004-0000-0000-0000-000000000000"), "income", 15000.00, "Work & Professional", lastWeek, "Freelance project payment", "Client: ABC Corp", false, nil},

		// Last month
		{uuid.MustParse("a6000001-0000-0000-0000-000000000000"), "expense", 5000.00, "Health & Medical", lastMonth, "Dentist appointment", "", false, nil},
		{uuid.MustParse("a6000002-0000-0000-0000-000000000000"), "expense", 1500.00, "Books & Media", lastMonth, "Books - programming", "", false, nil},

		// Soft-deleted
		{uuid.MustParse("a7000001-0000-0000-0000-000000000000"), "expense", 200.00, "Food & Dining", yesterday, "Duplicate entry", "Added by mistake", true, nil},
		{uuid.MustParse("a7000002-0000-0000-0000-000000000000"), "expense", 500.00, "Shopping", twoDaysAgo, "Cancelled order", "Returned item", true, nil},
	}

	for _, t := range personalTxs {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, is_deleted, recurring_expense_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			 ON CONFLICT (id) DO NOTHING`,
			t.id, UserAnkushID, t.txType, t.amount, t.category, t.date,
			t.description, t.notes, t.isDeleted, t.recurringExpID); err != nil {
			return fmt.Errorf("insert transaction %s: %w", t.description, err)
		}
	}
	log.Println("[seed] ✓ Personal transactions")

	// ── Groups ─────────────────────────────────────────────────────────────────
	for _, g := range []struct {
		id        uuid.UUID
		name      string
		createdBy uuid.UUID
	}{
		{GroupRoommatesID, "Roommates", UserAnkushID},
		{GroupTripGoaID, "Goa Trip 2024", UserAnkushID},
		{GroupOldTripID, "Manali Trip", UserPriyaID},
	} {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO groups (id, name, created_by) VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING`,
			g.id, g.name, g.createdBy); err != nil {
			return fmt.Errorf("insert group %s: %w", g.name, err)
		}
	}

	for _, m := range []struct {
		groupID, userID uuid.UUID
	}{
		{GroupRoommatesID, UserAnkushID}, {GroupRoommatesID, UserPriyaID}, {GroupRoommatesID, UserRahulID},
		{GroupTripGoaID, UserAnkushID}, {GroupTripGoaID, UserSaraID}, {GroupTripGoaID, UserPriyaID},
		{GroupOldTripID, UserPriyaID}, {GroupOldTripID, UserRahulID}, {GroupOldTripID, UserSaraID},
	} {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			m.groupID, m.userID); err != nil {
			return fmt.Errorf("insert group member: %w", err)
		}
	}
	log.Println("[seed] ✓ Groups & members")

	// ── Group transactions with splits ─────────────────────────────────────────
	type splitInput struct {
		userID uuid.UUID
		amount float64
	}
	type groupTxRow struct {
		id          uuid.UUID
		groupID     uuid.UUID
		paidBy      uuid.UUID
		total       float64
		category    string
		date        time.Time
		description string
		splits      []splitInput
	}

	groupTxs := []groupTxRow{
		// Roommates — Ankush paid rent deposit
		{
			uuid.MustParse("b1000001-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserAnkushID, 9000.00, "Housing", lastMonth, "Security Deposit Split",
			[]splitInput{{UserAnkushID, 3000.00}, {UserPriyaID, 3000.00}, {UserRahulID, 3000.00}},
		},
		// Roommates — Priya paid electricity
		{
			uuid.MustParse("b1000002-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserPriyaID, 1800.00, "Utilities", lastWeek, "Electricity Bill Feb",
			[]splitInput{{UserAnkushID, 600.00}, {UserPriyaID, 600.00}, {UserRahulID, 600.00}},
		},
		// Roommates — Rahul paid wifi
		{
			uuid.MustParse("b1000003-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserRahulID, 999.00, "Utilities", twoDaysAgo, "WiFi Bill March",
			[]splitInput{{UserAnkushID, 333.00}, {UserPriyaID, 333.00}, {UserRahulID, 333.00}},
		},
		// Roommates — Ankush paid groceries today
		{
			uuid.MustParse("b1000004-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserAnkushID, 2400.00, "Food & Dining", today, "Monthly groceries",
			[]splitInput{{UserAnkushID, 800.00}, {UserPriyaID, 800.00}, {UserRahulID, 800.00}},
		},
		// Goa Trip — Ankush paid hotel
		{
			uuid.MustParse("b2000001-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserAnkushID, 12000.00, "Travel", lastMonth, "Hotel booking - 3 nights",
			[]splitInput{{UserAnkushID, 4000.00}, {UserSaraID, 4000.00}, {UserPriyaID, 4000.00}},
		},
		// Goa Trip — Sara paid food
		{
			uuid.MustParse("b2000002-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserSaraID, 3600.00, "Food & Dining", lastMonth, "Beach shack dinners",
			[]splitInput{{UserAnkushID, 1200.00}, {UserSaraID, 1200.00}, {UserPriyaID, 1200.00}},
		},
		// Goa Trip — Priya paid activities
		{
			uuid.MustParse("b2000003-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserPriyaID, 4500.00, "Entertainment", lastMonth.AddDate(0, 0, 1), "Water sports & activities",
			[]splitInput{{UserAnkushID, 1500.00}, {UserSaraID, 1500.00}, {UserPriyaID, 1500.00}},
		},
		// Manali Trip (Ankush NOT a member)
		{
			uuid.MustParse("b3000001-0000-0000-0000-000000000000"),
			GroupOldTripID, UserPriyaID, 9000.00, "Travel", lastMonth.AddDate(0, -1, 0), "Bus tickets Manali",
			[]splitInput{{UserPriyaID, 3000.00}, {UserRahulID, 3000.00}, {UserSaraID, 3000.00}},
		},
	}

	for _, gt := range groupTxs {
		// Insert group_transaction
		var gtExists bool
		if err := database.Pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM group_transactions WHERE id = $1)`, gt.id).Scan(&gtExists); err != nil {
			return fmt.Errorf("check group tx exists: %w", err)
		}
		if gtExists {
			continue
		}

		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO group_transactions (id, group_id, paid_by_user_id, total_amount, category, date, description)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			gt.id, gt.groupID, gt.paidBy, gt.total, gt.category, gt.date, gt.description); err != nil {
			return fmt.Errorf("insert group transaction %s: %w", gt.description, err)
		}

		// For each split: create personal transaction + split row
		for i, sp := range gt.splits {
			splitID := uuid.MustParse(fmt.Sprintf("%s%02d000000-0000-0000-0000", gt.id.String()[:8], i+1))

			var personalTxID uuid.UUID
			if err := database.Pool.QueryRow(ctx,
				`INSERT INTO transactions (user_id, type, amount, category, date, description, group_transaction_id)
				 VALUES ($1,'expense',$2,$3,$4,$5,$6)
				 RETURNING id`,
				sp.userID, sp.amount, gt.category, gt.date, gt.description, gt.id,
			).Scan(&personalTxID); err != nil {
				return fmt.Errorf("insert personal tx for split %d of %s: %w", i, gt.description, err)
			}

			if _, err := database.Pool.Exec(ctx,
				`INSERT INTO group_transaction_splits (id, group_transaction_id, user_id, amount, transaction_id)
				 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				splitID, gt.id, sp.userID, sp.amount, personalTxID); err != nil {
				return fmt.Errorf("insert split %d of %s: %w", i, gt.description, err)
			}
		}
	}
	log.Println("[seed] ✓ Group transactions & splits")

	// ── Settlements ────────────────────────────────────────────────────────────
	// Priya settles ₹600 to Ankush (Roommates, partial)
	if _, err := database.Pool.Exec(ctx,
		`INSERT INTO settlements (id, group_id, from_user, to_user, amount)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		uuid.MustParse("c1000001-0000-0000-0000-000000000000"),
		GroupRoommatesID, UserPriyaID, UserAnkushID, 600.00); err != nil {
		return fmt.Errorf("insert settlement: %w", err)
	}

	// Sara settles ₹4000 to Ankush (Goa hotel)
	if _, err := database.Pool.Exec(ctx,
		`INSERT INTO settlements (id, group_id, from_user, to_user, amount)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		uuid.MustParse("c2000001-0000-0000-0000-000000000000"),
		GroupTripGoaID, UserSaraID, UserAnkushID, 4000.00); err != nil {
		return fmt.Errorf("insert settlement: %w", err)
	}
	log.Println("[seed] ✓ Settlements")

	log.Println("[seed] ✓ All test data seeded successfully")
	return nil
}

// Cleanup removes all seeded test data. Called on graceful shutdown.
func Cleanup(ctx context.Context, database *db.DB) {
	log.Println("[seed] Cleaning up test data...")

	userIDStrs := make([]string, len(seededUserIDs))
	for i, id := range seededUserIDs {
		userIDStrs[i] = "'" + id.String() + "'"
	}
	groupIDStrs := []string{
		"'" + GroupRoommatesID.String() + "'",
		"'" + GroupTripGoaID.String() + "'",
		"'" + GroupOldTripID.String() + "'",
	}

	cleanupSQL := []string{
		fmt.Sprintf(`DELETE FROM settlements WHERE group_id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM group_transaction_splits WHERE group_transaction_id IN (SELECT id FROM group_transactions WHERE group_id IN (%s))`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM transactions WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM group_transactions WHERE group_id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM group_members WHERE group_id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM groups WHERE id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM recurring_expenses WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM monthly_budgets WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM custom_categories WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM users WHERE id IN (%s)`, joinStrings(userIDStrs)),
	}

	for _, sql := range cleanupSQL {
		if _, err := database.Pool.Exec(ctx, sql); err != nil {
			log.Printf("[seed] Warning: cleanup query failed: %v\nSQL: %s", err, sql)
		}
	}

	log.Println("[seed] ✓ Test data cleaned up")
}

func intPtr(v int) *int { return &v }

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}
