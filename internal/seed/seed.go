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
	GroupOldTripID   = uuid.MustParse("11111111-0003-0003-0003-111111111111") // a group Ankush is not member of

	// Recurring expenses (Ankush)
	RecurringRentID       = uuid.MustParse("22222222-0001-0001-0001-222222222222")
	RecurringNetflixID    = uuid.MustParse("33333333-0002-0002-0002-333333333333")
	RecurringGymID        = uuid.MustParse("44444444-0003-0003-0003-444444444444")
	RecurringInactiveID   = uuid.MustParse("55555555-0004-0004-0004-555555555555")
)

// seededIDs tracks all test data record IDs for cleanup
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

	// Ankush: one hidden predefined category (Shopping)
	_, err := database.Pool.Exec(ctx,
		`UPDATE custom_categories SET is_hidden = true
		 WHERE user_id = $1 AND predefined_key = 'shopping'`,
		UserAnkushID)
	if err != nil {
		return fmt.Errorf("hide shopping category: %w", err)
	}

	// Ankush: two custom categories
	customCats := []struct {
		name  string
		icon  string
		color string
	}{
		{"Subscriptions", "antenna.radiowaves.left.and.right.circle.fill", "#6C5CE7"},
		{"Gym & Fitness", "figure.run.circle.fill", "#00B894"},
	}
	for _, cc := range customCats {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key)
			 VALUES ($1, $2, $3, $4, false, false, NULL)
			 ON CONFLICT (user_id, name) DO NOTHING`,
			UserAnkushID, cc.name, cc.icon, cc.color)
		if err != nil {
			return fmt.Errorf("insert custom category %s: %w", cc.name, err)
		}
	}
	log.Println("[seed] ✓ Categories")

	// ── Budgets (Ankush) ───────────────────────────────────────────────────────
	budgets := []struct {
		year  int
		month int
		limit float64
	}{
		{now.Year(), int(now.Month()), 30000.00},        // current month
		{now.Year(), int(now.Month()) - 1, 25000.00},    // last month (may wrap, handled by Postgres)
	}
	// Handle January edge case
	prevMonth := int(now.Month()) - 1
	prevYear := now.Year()
	if prevMonth == 0 {
		prevMonth = 12
		prevYear--
	}
	budgets = []struct {
		year  int
		month int
		limit float64
	}{
		{now.Year(), int(now.Month()), 30000.00},
		{prevYear, prevMonth, 25000.00},
	}

	for _, b := range budgets {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO monthly_budgets (user_id, year, month, budget_limit)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (user_id, year, month) DO NOTHING`,
			UserAnkushID, b.year, b.month, b.limit)
		if err != nil {
			return fmt.Errorf("insert budget: %w", err)
		}
	}
	log.Println("[seed] ✓ Budgets")

	// ── Recurring expenses (Ankush) ────────────────────────────────────────────
	recs := []struct {
		id          uuid.UUID
		name        string
		amount      float64
		category    string
		frequency   string
		dayOfMonth  *int
		daysOfWeek  []int
		startDate   time.Time
		endDate     *time.Time
		isActive    bool
		lastAdded   *time.Time
		notes       string
	}{
		{
			id:         RecurringRentID,
			name:       "House Rent",
			amount:     15000.00,
			category:   "Housing",
			frequency:  "monthly",
			dayOfMonth: intPtr(1),
			startDate:  lastMonth,
			isActive:   true,
			lastAdded:  &today,
			notes:      "Monthly rent for flat",
		},
		{
			id:        RecurringNetflixID,
			name:      "Netflix",
			amount:    649.00,
			category:  "Subscriptions",
			frequency: "monthly",
			dayOfMonth: intPtr(15),
			startDate:  lastMonth,
			isActive:   true,
			lastAdded:  &lastMonth,
			notes:      "Streaming subscription",
		},
		{
			id:         RecurringGymID,
			name:       "Gym Membership",
			amount:     1200.00,
			category:   "Gym & Fitness",
			frequency:  "monthly",
			dayOfMonth: intPtr(5),
			startDate:  lastMonth,
			isActive:   true,
			lastAdded:  &today,
			notes:      "Monthly gym fee",
		},
		{
			id:        RecurringInactiveID,
			name:      "Spotify",
			amount:    119.00,
			category:  "Subscriptions",
			frequency: "monthly",
			dayOfMonth: intPtr(20),
			startDate:  lastMonth.AddDate(-1, 0, 0),
			endDate:   &lastMonth,
			isActive:   false,
			lastAdded:  &lastMonth,
			notes:      "Cancelled — switched to YouTube Music",
		},
	}

	for _, r := range recs {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO recurring_expenses
			 (id, user_id, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			 ON CONFLICT (id) DO NOTHING`,
			r.id, UserAnkushID, r.name, r.amount, r.category, r.frequency,
			r.dayOfMonth, r.daysOfWeek, r.startDate, r.endDate, r.isActive, r.lastAdded, r.notes)
		if err != nil {
			return fmt.Errorf("insert recurring %s: %w", r.name, err)
		}
	}
	log.Println("[seed] ✓ Recurring expenses")

	// ── Personal expenses (Ankush) ─────────────────────────────────────────────
	type expenseRow struct {
		id                uuid.UUID
		amount            float64
		category          string
		date              time.Time
		description       string
		notes             string
		isDeleted         bool
		recurringExpID    *uuid.UUID
	}

	personalExpenses := []expenseRow{
		// Today
		{uuid.MustParse("a1000001-0000-0000-0000-000000000000"), 320.00, "Food & Dining", today, "Lunch - Subway", "", false, nil},
		{uuid.MustParse("a1000002-0000-0000-0000-000000000000"), 45.00, "Transport", today, "Auto to office", "", false, nil},
		{uuid.MustParse("a1000003-0000-0000-0000-000000000000"), 15000.00, "Housing", today, "House Rent - March", "Paid via UPI", false, &RecurringRentID},
		{uuid.MustParse("a1000004-0000-0000-0000-000000000000"), 1200.00, "Gym & Fitness", today, "Gym Membership March", "", false, &RecurringGymID},

		// Yesterday
		{uuid.MustParse("a2000001-0000-0000-0000-000000000000"), 580.00, "Food & Dining", yesterday, "Dinner - Barbeque Nation", "Split with friends", false, nil},
		{uuid.MustParse("a2000002-0000-0000-0000-000000000000"), 199.00, "Shopping", yesterday, "T-shirt from Myntra", "", false, nil},
		{uuid.MustParse("a2000003-0000-0000-0000-000000000000"), 60.00, "Transport", yesterday, "Cab to mall", "", false, nil},
		{uuid.MustParse("a2000004-0000-0000-0000-000000000000"), 250.00, "Health & Medical", yesterday, "Pharmacy - vitamins", "", false, nil},

		// 2 days ago
		{uuid.MustParse("a3000001-0000-0000-0000-000000000000"), 120.00, "Food & Dining", twoDaysAgo, "Coffee + snacks", "", false, nil},
		{uuid.MustParse("a3000002-0000-0000-0000-000000000000"), 1499.00, "Entertainment", twoDaysAgo, "Movie tickets (2x)", "", false, nil},
		{uuid.MustParse("a3000003-0000-0000-0000-000000000000"), 649.00, "Subscriptions", twoDaysAgo, "Netflix - March", "", false, &RecurringNetflixID},

		// 3 days ago
		{uuid.MustParse("a4000001-0000-0000-0000-000000000000"), 85.00, "Transport", threeDaysAgo, "Metro card recharge", "", false, nil},
		{uuid.MustParse("a4000002-0000-0000-0000-000000000000"), 450.00, "Food & Dining", threeDaysAgo, "Grocery shopping", "", false, nil},
		{uuid.MustParse("a4000003-0000-0000-0000-000000000000"), 2000.00, "Education", threeDaysAgo, "Udemy course", "Go programming advanced", false, nil},

		// Last week
		{uuid.MustParse("a5000001-0000-0000-0000-000000000000"), 3200.00, "Shopping", lastWeek, "Nike shoes", "", false, nil},
		{uuid.MustParse("a5000002-0000-0000-0000-000000000000"), 780.00, "Food & Dining", lastWeek, "Team lunch at office", "", false, nil},
		{uuid.MustParse("a5000003-0000-0000-0000-000000000000"), 400.00, "Utilities", lastWeek, "Electricity bill", "", false, nil},

		// Last month
		{uuid.MustParse("a6000001-0000-0000-0000-000000000000"), 5000.00, "Health & Medical", lastMonth, "Dentist appointment", "", false, nil},
		{uuid.MustParse("a6000002-0000-0000-0000-000000000000"), 1500.00, "Books & Media", lastMonth, "Books - programming", "", false, nil},

		// Soft-deleted (will show up with is_deleted=true filter)
		{uuid.MustParse("a7000001-0000-0000-0000-000000000000"), 200.00, "Food & Dining", yesterday, "Duplicate entry", "Added by mistake", true, nil},
		{uuid.MustParse("a7000002-0000-0000-0000-000000000000"), 500.00, "Shopping", twoDaysAgo, "Cancelled order", "Returned item", true, nil},
	}

	for _, e := range personalExpenses {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO expenses (id, user_id, amount, category, date, description, notes, is_deleted, recurring_expense_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			 ON CONFLICT (id) DO NOTHING`,
			e.id, UserAnkushID, e.amount, e.category, e.date, e.description, e.notes, e.isDeleted, e.recurringExpID)
		if err != nil {
			return fmt.Errorf("insert personal expense %s: %w", e.description, err)
		}
	}
	log.Println("[seed] ✓ Personal expenses")

	// ── Groups ─────────────────────────────────────────────────────────────────
	type groupRow struct {
		id        uuid.UUID
		name      string
		createdBy uuid.UUID
	}
	groups := []groupRow{
		{GroupRoommatesID, "Roommates", UserAnkushID},
		{GroupTripGoaID, "Goa Trip 2024", UserAnkushID},
		{GroupOldTripID, "Manali Trip", UserPriyaID}, // Ankush is NOT a member
	}

	for _, g := range groups {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO groups (id, name, created_by) VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING`,
			g.id, g.name, g.createdBy)
		if err != nil {
			return fmt.Errorf("insert group %s: %w", g.name, err)
		}
	}

	// Group members
	type memberRow struct {
		groupID uuid.UUID
		userID  uuid.UUID
	}
	members := []memberRow{
		// Roommates: Ankush, Priya, Rahul
		{GroupRoommatesID, UserAnkushID},
		{GroupRoommatesID, UserPriyaID},
		{GroupRoommatesID, UserRahulID},
		// Goa Trip: Ankush, Sara, Priya
		{GroupTripGoaID, UserAnkushID},
		{GroupTripGoaID, UserSaraID},
		{GroupTripGoaID, UserPriyaID},
		// Manali Trip (no Ankush): Priya, Rahul, Sara
		{GroupOldTripID, UserPriyaID},
		{GroupOldTripID, UserRahulID},
		{GroupOldTripID, UserSaraID},
	}

	for _, m := range members {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			m.groupID, m.userID)
		if err != nil {
			return fmt.Errorf("insert member: %w", err)
		}
	}
	log.Println("[seed] ✓ Groups & members")

	// ── Group expenses with splits ─────────────────────────────────────────────
	type groupExpense struct {
		id          uuid.UUID
		paidBy      uuid.UUID
		groupID     uuid.UUID
		groupName   string
		amount      float64
		category    string
		date        time.Time
		description string
		splits      []struct {
			userID uuid.UUID
			amount float64
		}
	}

	groupExpenses := []groupExpense{
		// Roommates — Ankush paid rent deposit
		{
			id: uuid.MustParse("b1000001-0000-0000-0000-000000000000"),
			paidBy: UserAnkushID, groupID: GroupRoommatesID, groupName: "Roommates",
			amount: 9000.00, category: "Housing", date: lastMonth,
			description: "Security Deposit Split",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 3000.00},
				{UserPriyaID, 3000.00},
				{UserRahulID, 3000.00},
			},
		},
		// Roommates — Priya paid electricity
		{
			id: uuid.MustParse("b1000002-0000-0000-0000-000000000000"),
			paidBy: UserPriyaID, groupID: GroupRoommatesID, groupName: "Roommates",
			amount: 1800.00, category: "Utilities", date: lastWeek,
			description: "Electricity Bill Feb",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 600.00},
				{UserPriyaID, 600.00},
				{UserRahulID, 600.00},
			},
		},
		// Roommates — Rahul paid wifi
		{
			id: uuid.MustParse("b1000003-0000-0000-0000-000000000000"),
			paidBy: UserRahulID, groupID: GroupRoommatesID, groupName: "Roommates",
			amount: 999.00, category: "Utilities", date: twoDaysAgo,
			description: "WiFi Bill March",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 333.00},
				{UserPriyaID, 333.00},
				{UserRahulID, 333.00},
			},
		},
		// Roommates — Ankush paid groceries today
		{
			id: uuid.MustParse("b1000004-0000-0000-0000-000000000000"),
			paidBy: UserAnkushID, groupID: GroupRoommatesID, groupName: "Roommates",
			amount: 2400.00, category: "Food & Dining", date: today,
			description: "Monthly groceries",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 800.00},
				{UserPriyaID, 800.00},
				{UserRahulID, 800.00},
			},
		},

		// Goa Trip — Ankush paid hotel
		{
			id: uuid.MustParse("b2000001-0000-0000-0000-000000000000"),
			paidBy: UserAnkushID, groupID: GroupTripGoaID, groupName: "Goa Trip 2024",
			amount: 12000.00, category: "Travel", date: lastMonth,
			description: "Hotel booking - 3 nights",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 4000.00},
				{UserSaraID, 4000.00},
				{UserPriyaID, 4000.00},
			},
		},
		// Goa Trip — Sara paid food
		{
			id: uuid.MustParse("b2000002-0000-0000-0000-000000000000"),
			paidBy: UserSaraID, groupID: GroupTripGoaID, groupName: "Goa Trip 2024",
			amount: 3600.00, category: "Food & Dining", date: lastMonth,
			description: "Beach shack dinners",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 1200.00},
				{UserSaraID, 1200.00},
				{UserPriyaID, 1200.00},
			},
		},
		// Goa Trip — Priya paid activities
		{
			id: uuid.MustParse("b2000003-0000-0000-0000-000000000000"),
			paidBy: UserPriyaID, groupID: GroupTripGoaID, groupName: "Goa Trip 2024",
			amount: 4500.00, category: "Entertainment", date: lastMonth.AddDate(0, 0, 1),
			description: "Water sports & activities",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserAnkushID, 1500.00},
				{UserSaraID, 1500.00},
				{UserPriyaID, 1500.00},
			},
		},

		// Manali Trip (Ankush NOT a member) — Priya paid
		{
			id: uuid.MustParse("b3000001-0000-0000-0000-000000000000"),
			paidBy: UserPriyaID, groupID: GroupOldTripID, groupName: "Manali Trip",
			amount: 9000.00, category: "Travel", date: lastMonth.AddDate(0, -1, 0),
			description: "Bus tickets Manali",
			splits: []struct {
				userID uuid.UUID
				amount float64
			}{
				{UserPriyaID, 3000.00},
				{UserRahulID, 3000.00},
				{UserSaraID, 3000.00},
			},
		},
	}

	for _, ge := range groupExpenses {
		_, err := database.Pool.Exec(ctx,
			`INSERT INTO expenses (id, user_id, amount, category, date, description, group_id, group_name, is_deleted)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,false)
			 ON CONFLICT (id) DO NOTHING`,
			ge.id, ge.paidBy, ge.amount, ge.category, ge.date, ge.description, ge.groupID, ge.groupName)
		if err != nil {
			return fmt.Errorf("insert group expense %s: %w", ge.description, err)
		}

		for _, sp := range ge.splits {
			_, err := database.Pool.Exec(ctx,
				`INSERT INTO expense_splits (expense_id, user_id, amount)
				 VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				ge.id, sp.userID, sp.amount)
			if err != nil {
				return fmt.Errorf("insert split for %s: %w", ge.description, err)
			}
		}
	}
	log.Println("[seed] ✓ Group expenses & splits")

	// ── Settlements ────────────────────────────────────────────────────────────
	// Priya settles ₹600 to Ankush for Roommates (partial)
	_, err = database.Pool.Exec(ctx,
		`INSERT INTO settlements (id, group_id, from_user, to_user, amount)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		uuid.MustParse("c1000001-0000-0000-0000-000000000000"),
		GroupRoommatesID, UserPriyaID, UserAnkushID, 600.00)
	if err != nil {
		return fmt.Errorf("insert settlement: %w", err)
	}

	// Sara settles ₹4000 to Ankush for Goa trip (hotel)
	_, err = database.Pool.Exec(ctx,
		`INSERT INTO settlements (id, group_id, from_user, to_user, amount)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		uuid.MustParse("c2000001-0000-0000-0000-000000000000"),
		GroupTripGoaID, UserSaraID, UserAnkushID, 4000.00)
	if err != nil {
		return fmt.Errorf("insert settlement: %w", err)
	}
	log.Println("[seed] ✓ Settlements")

	log.Println("[seed] ✓ All test data seeded successfully")
	return nil
}

// Cleanup removes all seeded test data from the database.
// Called on graceful server shutdown.
func Cleanup(ctx context.Context, database *db.DB) {
	log.Println("[seed] Cleaning up test data...")

	// Delete in reverse dependency order; CASCADE handles most child rows
	// but we name them explicitly for clarity.

	tables := []string{
		"settlements",
		"expense_splits",
		"expenses",
		"group_members",
		"groups",
		"recurring_expenses",
		"monthly_budgets",
		"custom_categories",
		"users",
	}

	// We delete by matching seeded user IDs (all data is owned by seeded users)
	userIDStrs := make([]string, len(seededUserIDs))
	for i, id := range seededUserIDs {
		userIDStrs[i] = "'" + id.String() + "'"
	}

	// Groups owned by seeded users (for settlements/group_members)
	groupIDStrs := []string{
		"'" + GroupRoommatesID.String() + "'",
		"'" + GroupTripGoaID.String() + "'",
		"'" + GroupOldTripID.String() + "'",
	}

	cleanupSQL := []string{
		fmt.Sprintf(`DELETE FROM settlements WHERE group_id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM expense_splits WHERE expense_id IN (SELECT id FROM expenses WHERE user_id IN (%s))`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM expenses WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM group_members WHERE group_id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM groups WHERE id IN (%s)`, joinStrings(groupIDStrs)),
		fmt.Sprintf(`DELETE FROM recurring_expenses WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM monthly_budgets WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM custom_categories WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM users WHERE id IN (%s)`, joinStrings(userIDStrs)),
	}

	_ = tables // suppress unused warning

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
