package financial

import (
	"errors"
	"regexp"

	"github.com/shopspring/decimal"
)

var (
	errInvalidAmount = errors.New("invalid monetary amount")
	validAmountText  = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,17})(?:\.[0-9]{1,2})?$`)
)

// parsePostingAmount constrains commands to positive, non-exponential values
// representable by the ledger's NUMERIC(20,2) columns.
func parsePostingAmount(raw string) (decimal.Decimal, error) {
	if !validAmountText.MatchString(raw) {
		return decimal.Zero, errInvalidAmount
	}
	amount, err := decimal.NewFromString(raw)
	if err != nil || !amount.IsPositive() {
		return decimal.Zero, errInvalidAmount
	}
	return amount, nil
}
