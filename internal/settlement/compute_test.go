package settlement

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func TestComputeSettlementEffect(t *testing.T) {
	tests := []struct {
		name             string
		pairwiseDebt     decimal.Decimal
		settlementAmount decimal.Decimal
		wantDebtCleared  decimal.Decimal
		wantExcess       decimal.Decimal
	}{
		{
			name:             "zero existing debt — full amount is excess",
			pairwiseDebt:     dec("0"),
			settlementAmount: dec("100"),
			wantDebtCleared:  dec("0"),
			wantExcess:       dec("100"),
		},
		{
			name:             "partial debt — some cleared, rest is excess",
			pairwiseDebt:     dec("40"),
			settlementAmount: dec("100"),
			wantDebtCleared:  dec("40"),
			wantExcess:       dec("60"),
		},
		{
			name:             "exact debt match — all cleared, zero excess",
			pairwiseDebt:     dec("100"),
			settlementAmount: dec("100"),
			wantDebtCleared:  dec("100"),
			wantExcess:       dec("0"),
		},
		{
			name:             "settlement less than debt — partial clear, no excess",
			pairwiseDebt:     dec("100"),
			settlementAmount: dec("60"),
			wantDebtCleared:  dec("60"),
			wantExcess:       dec("0"),
		},
		{
			name:             "inverted direction — paying party is the creditor, full amount is excess",
			pairwiseDebt:     dec("-30"),
			settlementAmount: dec("50"),
			wantDebtCleared:  dec("0"),
			wantExcess:       dec("50"),
		},
		{
			name:             "excess over debt — debt cleared and excess remain",
			pairwiseDebt:     dec("25"),
			settlementAmount: dec("75"),
			wantDebtCleared:  dec("25"),
			wantExcess:       dec("50"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			effect := ComputeSettlementEffect(tc.pairwiseDebt, tc.settlementAmount)
			assert.True(t, tc.wantDebtCleared.Equal(effect.DebtCleared),
				"DebtCleared: want %s got %s", tc.wantDebtCleared, effect.DebtCleared)
			assert.True(t, tc.wantExcess.Equal(effect.Excess),
				"Excess: want %s got %s", tc.wantExcess, effect.Excess)
			// Invariant: DebtCleared + Excess == settlementAmount
			total := effect.DebtCleared.Add(effect.Excess)
			assert.True(t, total.Equal(tc.settlementAmount),
				"DebtCleared+Excess should equal settlementAmount: want %s got %s", tc.settlementAmount, total)
		})
	}
}
