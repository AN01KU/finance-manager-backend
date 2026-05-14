package settlement

import "github.com/shopspring/decimal"

// SettlementEffect describes the outcome of applying a settlement against an
// existing pairwise debt.
type SettlementEffect struct {
	// DebtCleared is the portion of settlementAmount that cancels existing debt.
	// It is min(max(0, pairwiseDebt), settlementAmount).
	DebtCleared decimal.Decimal
	// Excess is the portion of settlementAmount beyond the existing debt.
	// When positive, personal income/expense transactions must be created.
	Excess decimal.Decimal
}

// ComputeSettlementEffect determines how much of settlementAmount clears
// existing debt vs. creates fresh cash flow (excess). pairwiseDebt is the
// net amount fromUser owes toUser before this settlement:
//
//   - positive  → fromUser owes toUser
//   - zero      → even; full amount is excess
//   - negative  → toUser owes fromUser (inverted direction); full amount is excess
func ComputeSettlementEffect(pairwiseDebt, settlementAmount decimal.Decimal) SettlementEffect {
	covered := pairwiseDebt
	if covered.IsNegative() {
		covered = decimal.Zero
	}
	excess := settlementAmount.Sub(covered)
	if excess.IsNegative() {
		excess = decimal.Zero
	}
	debtCleared := settlementAmount.Sub(excess)
	return SettlementEffect{
		DebtCleared: debtCleared,
		Excess:      excess,
	}
}
