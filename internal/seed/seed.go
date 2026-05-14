//go:build !release

package seed

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
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

	// Sync sessions
	SyncSessionAnkushID = uuid.MustParse("99999999-0001-0001-0001-999999999999")
	SyncSessionPriyaID  = uuid.MustParse("99999999-0002-0002-0002-999999999999")
	SyncSessionRahulID  = uuid.MustParse("99999999-0003-0003-0003-999999999999")
	SyncSessionSaraID   = uuid.MustParse("99999999-0004-0004-0004-999999999999")

	// Recurring transactions (Ankush)
	RecurringRentID     = uuid.MustParse("22222222-0001-0001-0001-222222222222")
	RecurringNetflixID  = uuid.MustParse("33333333-0002-0002-0002-333333333333")
	RecurringGymID      = uuid.MustParse("44444444-0003-0003-0003-444444444444")
	RecurringInactiveID = uuid.MustParse("55555555-0004-0004-0004-555555555555")
	RecurringSalaryID   = uuid.MustParse("66666666-0005-0005-0005-666666666666")
)

var seededUserIDs = []uuid.UUID{UserAnkushID, UserPriyaID, UserRahulID, UserSaraID}

// Seed inserts all test data into the database.
// It is idempotent — safe to call on every startup.
func Seed(ctx context.Context, database *db.DB) error {
	slog.Info("seed: seeding test data")

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
			`INSERT INTO users (id, email, username, password_hash, email_verified)
			 VALUES ($1, $2, $3, $4, TRUE)
			 ON CONFLICT (id) DO NOTHING`,
			u.id, u.email, u.username, string(hash))
		if err != nil {
			return fmt.Errorf("insert user %s: %w", u.email, err)
		}
	}
	slog.Debug("seed: users created")

	// ── Sync sessions ─────────────────────────────────────────────────────────
	syncSessions := []struct {
		id     uuid.UUID
		userID uuid.UUID
	}{
		{SyncSessionAnkushID, UserAnkushID},
		{SyncSessionPriyaID, UserPriyaID},
		{SyncSessionRahulID, UserRahulID},
		{SyncSessionSaraID, UserSaraID},
	}

	for _, ss := range syncSessions {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO sync_sessions (id, user_id) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
			ss.id, ss.userID); err != nil {
			return fmt.Errorf("insert sync session for user %s: %w", ss.userID, err)
		}
	}
	slog.Debug("seed: sync sessions created")

	// ── Category overrides & custom categories ────────────────────────────────
	// Predefined categories live in the predefined_categories table — rows here
	// are user overrides (is_predefined = TRUE + predefined_key) or fully custom
	// (is_predefined = FALSE + predefined_key = NULL). Icons must be kebab-case
	// keys matching one of the embedded SVG icons.
	type catRow struct {
		userID        uuid.UUID
		name          string
		icon          string
		color         string
		isHidden      bool
		isPredefined  bool
		predefinedKey *string
	}
	strPtr := func(s string) *string { return &s }

	categoryRows := []catRow{
		// Ankush: override — hide Shopping
		{UserAnkushID, "Shopping", "shopping", "#FFEAA7", true, true, strPtr("shopping")},
		// Ankush: override — recolor Food & Dining
		{UserAnkushID, "Food & Dining", "food-dining", "#FF4757", false, true, strPtr("food-dining")},
		// Ankush: custom categories
		{UserAnkushID, "Crypto", "investments", "#F0B90B", false, false, nil},
		{UserAnkushID, "Side Hustle", "freelance", "#27AE60", false, false, nil},

		// Priya: override — rename & recolor Transport
		{UserPriyaID, "Commute", "public-transit", "#1ABC9C", false, true, strPtr("transport")},
		// Priya: custom category
		{UserPriyaID, "Skincare", "personal-care", "#FF69B4", false, false, nil},

		// Rahul: override — hide Entertainment
		{UserRahulID, "Entertainment", "entertainment", "#BC6C25", true, true, strPtr("entertainment")},
	}

	for _, cc := range categoryRows {
		var catKey string
		if cc.isPredefined && cc.predefinedKey != nil {
			catKey = "oc-" + *cc.predefinedKey
		} else {
			catKey = "cc-" + uuid.New().String()
		}
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO custom_categories (user_id, name, icon, color, is_hidden, is_predefined, predefined_key, key)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (user_id, name) DO NOTHING`,
			cc.userID, cc.name, cc.icon, cc.color, cc.isHidden, cc.isPredefined, cc.predefinedKey, catKey); err != nil {
			return fmt.Errorf("insert category %s: %w", cc.name, err)
		}
	}
	slog.Debug("seed: categories created")

	// ── Budget (Ankush) ────────────────────────────────────────────────────────
	if _, err := database.Pool.Exec(ctx,
		`UPDATE users SET monthly_budget = 30000.00 WHERE id = $1`, UserAnkushID); err != nil {
		return fmt.Errorf("set budget: %w", err)
	}
	slog.Debug("seed: budget created")

	// ── Recurring transactions (Ankush) ────────────────────────────────────────
	recs := []struct {
		id         uuid.UUID
		txType     string
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
		{RecurringRentID, "expense", "House Rent", 15000.00, "housing-rent", "monthly", intPtr(1), nil, lastMonth, nil, true, &today, "Monthly rent for flat"},
		{RecurringNetflixID, "expense", "Netflix", 649.00, "streaming", "monthly", intPtr(15), nil, lastMonth, nil, true, &lastMonth, "Streaming subscription"},
		{RecurringGymID, "expense", "Gym Membership", 1200.00, "gym-fitness", "monthly", intPtr(5), nil, lastMonth, nil, true, &today, "Monthly gym fee"},
		{RecurringInactiveID, "expense", "Spotify", 119.00, "streaming", "monthly", intPtr(20), nil, lastMonth.AddDate(-1, 0, 0), &lastMonth, false, &lastMonth, "Cancelled — switched to YouTube Music"},
		{RecurringSalaryID, "income", "Monthly Salary", 85000.00, "salary-income", "monthly", intPtr(1), nil, lastMonth, nil, true, &today, "Net salary after TDS"},
	}

	for _, r := range recs {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO recurring_transactions
			 (id, user_id, type, name, amount, category, frequency, day_of_month, days_of_week, start_date, end_date, is_active, last_added_date, notes)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			 ON CONFLICT (id) DO NOTHING`,
			r.id, UserAnkushID, r.txType, r.name, r.amount, r.category, r.frequency,
			r.dayOfMonth, r.daysOfWeek, r.startDate, r.endDate, r.isActive, r.lastAdded, r.notes); err != nil {
			return fmt.Errorf("insert recurring %s: %w", r.name, err)
		}
	}
	slog.Debug("seed: recurring transactions created")

	// ── Personal transactions (Ankush) ─────────────────────────────────────────
	// type: 'expense' or 'income'
	type txRow struct {
		id            uuid.UUID
		txType        string
		amount        float64
		category      string
		date          time.Time
		description   string
		notes         string
		isDeleted     bool
		recurringTxID *uuid.UUID
	}

	personalTxs := []txRow{
		// Today — expenses
		{uuid.MustParse("a1000001-0000-0000-0000-000000000000"), "expense", 320.00, "food-dining", today, "Lunch - Subway", "", false, nil},
		{uuid.MustParse("a1000002-0000-0000-0000-000000000000"), "expense", 45.00, "transport", today, "Auto to office", "", false, nil},
		{uuid.MustParse("a1000003-0000-0000-0000-000000000000"), "expense", 15000.00, "housing-rent", today, "House Rent - March", "Paid via UPI", false, &RecurringRentID},
		{uuid.MustParse("a1000004-0000-0000-0000-000000000000"), "expense", 1200.00, "gym-fitness", today, "Gym Membership March", "", false, &RecurringGymID},
		// Today — income (salary)
		{uuid.MustParse("a1000005-0000-0000-0000-000000000000"), "income", 85000.00, "salary-income", today, "March Salary", "Net after TDS", false, &RecurringSalaryID},

		// Yesterday
		{uuid.MustParse("a2000001-0000-0000-0000-000000000000"), "expense", 580.00, "dining-out", yesterday, "Dinner - Barbeque Nation", "Split with friends", false, nil},
		{uuid.MustParse("a2000002-0000-0000-0000-000000000000"), "expense", 199.00, "clothing", yesterday, "T-shirt from Myntra", "", false, nil},
		{uuid.MustParse("a2000003-0000-0000-0000-000000000000"), "expense", 60.00, "transport", yesterday, "Cab to mall", "", false, nil},
		{uuid.MustParse("a2000004-0000-0000-0000-000000000000"), "expense", 250.00, "pharmacy", yesterday, "Pharmacy - vitamins", "", false, nil},

		// 2 days ago
		{uuid.MustParse("a3000001-0000-0000-0000-000000000000"), "expense", 120.00, "coffee-cafe", twoDaysAgo, "Coffee + snacks", "", false, nil},
		{uuid.MustParse("a3000002-0000-0000-0000-000000000000"), "expense", 1499.00, "entertainment", twoDaysAgo, "Movie tickets (2x)", "", false, nil},
		{uuid.MustParse("a3000003-0000-0000-0000-000000000000"), "expense", 649.00, "streaming", twoDaysAgo, "Netflix - March", "", false, &RecurringNetflixID},

		// 3 days ago
		{uuid.MustParse("a4000001-0000-0000-0000-000000000000"), "expense", 85.00, "public-transit", threeDaysAgo, "Metro card recharge", "", false, nil},
		{uuid.MustParse("a4000002-0000-0000-0000-000000000000"), "expense", 450.00, "groceries", threeDaysAgo, "Grocery shopping", "", false, nil},
		{uuid.MustParse("a4000003-0000-0000-0000-000000000000"), "expense", 2000.00, "online-courses", threeDaysAgo, "Udemy course", "Go programming advanced", false, nil},

		// Last week
		{uuid.MustParse("a5000001-0000-0000-0000-000000000000"), "expense", 3200.00, "shopping", lastWeek, "Nike shoes", "", false, nil},
		{uuid.MustParse("a5000002-0000-0000-0000-000000000000"), "expense", 780.00, "dining-out", lastWeek, "Team lunch at office", "", false, nil},
		{uuid.MustParse("a5000003-0000-0000-0000-000000000000"), "expense", 400.00, "electricity-gas", lastWeek, "Electricity bill", "", false, nil},
		// Last week — freelance income
		{uuid.MustParse("a5000004-0000-0000-0000-000000000000"), "income", 15000.00, "freelance", lastWeek, "Freelance project payment", "Client: ABC Corp", false, nil},

		// Last month
		{uuid.MustParse("a6000001-0000-0000-0000-000000000000"), "expense", 5000.00, "health-medical", lastMonth, "Dentist appointment", "", false, nil},
		{uuid.MustParse("a6000002-0000-0000-0000-000000000000"), "expense", 1500.00, "books-reading", lastMonth, "Books - programming", "", false, nil},

		// Soft-deleted
		{uuid.MustParse("a7000001-0000-0000-0000-000000000000"), "expense", 200.00, "food-dining", yesterday, "Duplicate entry", "Added by mistake", true, nil},
		{uuid.MustParse("a7000002-0000-0000-0000-000000000000"), "expense", 500.00, "shopping", twoDaysAgo, "Cancelled order", "Returned item", true, nil},
	}

	for _, t := range personalTxs {
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO transactions (id, user_id, type, amount, category, date, description, notes, is_deleted, recurring_transaction_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			 ON CONFLICT (id) DO NOTHING`,
			t.id, UserAnkushID, t.txType, t.amount, t.category, t.date,
			t.description, t.notes, t.isDeleted, t.recurringTxID); err != nil {
			return fmt.Errorf("insert transaction %s: %w", t.description, err)
		}
	}
	slog.Debug("seed: personal transactions created")

	// ── Groups ─────────────────────────────────────────────────────────────────
	for _, g := range []struct {
		id        uuid.UUID
		name      string
		createdBy uuid.UUID
	}{
		{GroupRoommatesID, "Roommates", UserAnkushID},
		{GroupTripGoaID, "Goa Trip 2026", UserAnkushID},
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
	slog.Debug("seed: groups and members created")

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
			GroupRoommatesID, UserAnkushID, 9000.00, "housing-rent", lastMonth, "Security Deposit Split",
			[]splitInput{{UserAnkushID, 3000.00}, {UserPriyaID, 3000.00}, {UserRahulID, 3000.00}},
		},
		// Roommates — Priya paid electricity
		{
			uuid.MustParse("b1000002-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserPriyaID, 1800.00, "electricity-gas", lastWeek, "Electricity Bill Feb",
			[]splitInput{{UserAnkushID, 600.00}, {UserPriyaID, 600.00}, {UserRahulID, 600.00}},
		},
		// Roommates — Rahul paid wifi
		{
			uuid.MustParse("b1000003-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserRahulID, 999.00, "phone-internet", twoDaysAgo, "WiFi Bill March",
			[]splitInput{{UserAnkushID, 333.00}, {UserPriyaID, 333.00}, {UserRahulID, 333.00}},
		},
		// Roommates — Ankush paid groceries today
		{
			uuid.MustParse("b1000004-0000-0000-0000-000000000000"),
			GroupRoommatesID, UserAnkushID, 2400.00, "groceries", today, "Monthly groceries",
			[]splitInput{{UserAnkushID, 800.00}, {UserPriyaID, 800.00}, {UserRahulID, 800.00}},
		},
		// Goa Trip — Ankush paid hotel
		{
			uuid.MustParse("b2000001-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserAnkushID, 12000.00, "travel", lastMonth, "Hotel booking - 3 nights",
			[]splitInput{{UserAnkushID, 4000.00}, {UserSaraID, 4000.00}, {UserPriyaID, 4000.00}},
		},
		// Goa Trip — Sara paid food
		{
			uuid.MustParse("b2000002-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserSaraID, 3600.00, "food-dining", lastMonth, "Beach shack dinners",
			[]splitInput{{UserAnkushID, 1200.00}, {UserSaraID, 1200.00}, {UserPriyaID, 1200.00}},
		},
		// Goa Trip — Priya paid activities
		{
			uuid.MustParse("b2000003-0000-0000-0000-000000000000"),
			GroupTripGoaID, UserPriyaID, 4500.00, "entertainment", lastMonth.AddDate(0, 0, 1), "Water sports & activities",
			[]splitInput{{UserAnkushID, 1500.00}, {UserSaraID, 1500.00}, {UserPriyaID, 1500.00}},
		},
		// Manali Trip (Ankush NOT a member)
		{
			uuid.MustParse("b3000001-0000-0000-0000-000000000000"),
			GroupOldTripID, UserPriyaID, 9000.00, "travel", lastMonth.AddDate(0, -1, 0), "Bus tickets Manali",
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

		// For each split: create a personal expense transaction and insert the split row.
		// All members (including the payer) record their split share as the personal transaction amount.
		for _, sp := range gt.splits {
			txAmount := sp.amount

			var memberTxID uuid.UUID
			if err := database.Pool.QueryRow(ctx,
				`INSERT INTO transactions (user_id, type, amount, category, date, description, group_transaction_id)
				 VALUES ($1,'expense',$2,$3,$4,$5,$6)
				 RETURNING id`,
				sp.userID, txAmount, gt.category, gt.date, gt.description, gt.id,
			).Scan(&memberTxID); err != nil {
				return fmt.Errorf("insert personal tx for member of %s: %w", gt.description, err)
			}

			splitID := uuid.New()
			if _, err := database.Pool.Exec(ctx,
				`INSERT INTO group_transaction_splits (id, group_transaction_id, user_id, amount, transaction_id)
				 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				splitID, gt.id, sp.userID, sp.amount, &memberTxID); err != nil {
				return fmt.Errorf("insert split of %s: %w", gt.description, err)
			}
		}
	}
	slog.Debug("seed: group transactions and splits created")

	// ── Settlements ────────────────────────────────────────────────────────────
	type settlementRow struct {
		id      uuid.UUID
		groupID uuid.UUID
		from    uuid.UUID
		to      uuid.UUID
		amount  float64
		date    time.Time
	}

	settlements := []settlementRow{
		// Priya settles ₹600 to Ankush (Roommates, partial)
		{uuid.MustParse("c1000001-0000-0000-0000-000000000000"), GroupRoommatesID, UserPriyaID, UserAnkushID, 600.00, yesterday},
		// Sara settles ₹4000 to Ankush (Goa — hotel share, full)
		{uuid.MustParse("c2000001-0000-0000-0000-000000000000"), GroupTripGoaID, UserSaraID, UserAnkushID, 4000.00, lastWeek},
		// Ankush settles ₹600 to Priya (Roommates — electricity share)
		{uuid.MustParse("c3000001-0000-0000-0000-000000000000"), GroupRoommatesID, UserAnkushID, UserPriyaID, 600.00, twoDaysAgo},
		// Ankush settles ₹1500 to Priya (Goa — activities share)
		{uuid.MustParse("c4000001-0000-0000-0000-000000000000"), GroupTripGoaID, UserAnkushID, UserPriyaID, 1500.00, threeDaysAgo},
		// Priya settles ₹1200 to Ankush (Goa — hotel partial)
		{uuid.MustParse("c5000001-0000-0000-0000-000000000000"), GroupTripGoaID, UserPriyaID, UserAnkushID, 1200.00, lastWeek},
		// Sara settles ₹600 to Priya (Goa — activities share)
		{uuid.MustParse("c6000001-0000-0000-0000-000000000000"), GroupTripGoaID, UserSaraID, UserPriyaID, 600.00, twoDaysAgo},
		// Rahul settles ₹333 to Ankush (Roommates — wifi share)
		{uuid.MustParse("c7000001-0000-0000-0000-000000000000"), GroupRoommatesID, UserRahulID, UserAnkushID, 333.00, yesterday},
	}

	for _, st := range settlements {
		var exists bool
		if err := database.Pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM settlements WHERE id = $1)`, st.id).Scan(&exists); err != nil {
			return fmt.Errorf("check settlement exists: %w", err)
		}
		if exists {
			continue
		}

		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO settlements (id, group_id, from_user, to_user, amount)
			 VALUES ($1,$2,$3,$4,$5)`,
			st.id, st.groupID, st.from, st.to, st.amount); err != nil {
			return fmt.Errorf("insert settlement: %w", err)
		}

		// Income transaction for to_user (received money)
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO transactions (user_id, type, amount, category, date, description, group_id, settlement_id)
			 VALUES ($1, 'income', $2, 'other', $3, 'Settlement received', $4, $5)`,
			st.to, st.amount, st.date, st.groupID, st.id); err != nil {
			return fmt.Errorf("insert settlement income tx: %w", err)
		}

		// Expense transaction for from_user (paid money)
		if _, err := database.Pool.Exec(ctx,
			`INSERT INTO transactions (user_id, type, amount, category, date, description, group_id, settlement_id)
			 VALUES ($1, 'expense', $2, 'other', $3, 'Settlement paid', $4, $5)`,
			st.from, st.amount, st.date, st.groupID, st.id); err != nil {
			return fmt.Errorf("insert settlement expense tx: %w", err)
		}
	}
	slog.Debug("seed: settlements created")

	slog.Info("seed: all test data seeded successfully")
	return nil
}

// Cleanup removes all seeded test data. Called on graceful shutdown.
func Cleanup(ctx context.Context, database *db.DB) {
	slog.Info("seed: cleaning up test data")

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
		fmt.Sprintf(`DELETE FROM recurring_transactions WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM custom_categories WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM sync_sessions WHERE user_id IN (%s)`, joinStrings(userIDStrs)),
		fmt.Sprintf(`DELETE FROM users WHERE id IN (%s)`, joinStrings(userIDStrs)),
	}

	for _, sql := range cleanupSQL {
		if _, err := database.Pool.Exec(ctx, sql); err != nil {
			slog.Warn("seed: cleanup query failed", applog.KeyError, err, "sql", sql)
		}
	}

	slog.Info("seed: test data cleaned up")
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
