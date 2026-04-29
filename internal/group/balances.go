package group

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type payerEntry struct {
	paidBy uuid.UUID
	amount decimal.Decimal
}

type splitEntry struct {
	userID uuid.UUID
	amount decimal.Decimal
}

type settlementEntry struct {
	from, to uuid.UUID
	amount   decimal.Decimal
}

// computeBalances calculates net balance per member.
// Positive = owed money (paid more than share), Negative = owes money.
func computeBalances(members []GroupMember, payers []payerEntry, splits []splitEntry, settlements []settlementEntry) []Balance {
	bal := make(map[uuid.UUID]decimal.Decimal)
	for _, m := range members {
		bal[m.UserID] = decimal.Zero
	}

	for _, p := range payers {
		if _, ok := bal[p.paidBy]; ok {
			bal[p.paidBy] = bal[p.paidBy].Add(p.amount)
		}
	}

	for _, s := range splits {
		if _, ok := bal[s.userID]; ok {
			bal[s.userID] = bal[s.userID].Sub(s.amount)
		}
	}

	// Settlements: from_user paid their debt (+), to_user received (-)
	for _, s := range settlements {
		if _, ok := bal[s.from]; ok {
			bal[s.from] = bal[s.from].Add(s.amount)
		}
		if _, ok := bal[s.to]; ok {
			bal[s.to] = bal[s.to].Sub(s.amount)
		}
	}

	balances := []Balance{}
	for uid, amt := range bal {
		// Round to 2 decimal places before converting to float to suppress
		// float residuals (e.g. 0.0000001) from accumulated decimal arithmetic.
		// TODO(R2): per-currency precision (JPY=0, KWD=3) once multi-currency
		// support lands.
		balances = append(balances, Balance{UserID: uid, Amount: amt.Round(2).InexactFloat64()})
	}
	return balances
}
