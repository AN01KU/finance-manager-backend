package helpers

import (
	"strings"

	"github.com/shopspring/decimal"
)

type StringDecimal struct {
	decimal.Decimal
}

func (d StringDecimal) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}

func (d *StringDecimal) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	dec, err := decimal.NewFromString(s)
	if err != nil {
		return err
	}
	d.Decimal = dec
	return nil
}
