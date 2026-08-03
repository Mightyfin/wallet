package financial

import "testing"

func TestParsePostingAmountBoundaries(t *testing.T) {
	valid := []string{"0.01", "1", "1.0", "1.00", "999999999999999999.99"}
	for _, input := range valid {
		if _, err := parsePostingAmount(input); err != nil {
			t.Errorf("valid amount %q rejected: %v", input, err)
		}
	}
	invalid := []string{
		"", "0", "0.00", "-1.00", "+1.00", "01.00", "1.", ".50", "1.001",
		"1e2", "1E100", "NaN", "Inf", " 1.00", "1.00 ",
		"1000000000000000000.00",
	}
	for _, input := range invalid {
		if _, err := parsePostingAmount(input); err == nil {
			t.Errorf("invalid amount %q accepted", input)
		}
	}
}
