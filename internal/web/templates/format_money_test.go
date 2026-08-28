package templates

import "testing"

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{1500, "usd", "15.00 $"},
		{1500, "USD", "15.00 $"}, // ads table stores uppercase
		{1550, "eur", "15.50 €"},
		{0, "usd", "0.00 $"},
		{5, "usd", "0.05 $"},          // sub-dime cents keep their leading zero
		{123456, "usd", "1 234.56 $"}, // thin-space thousands like formatTotal
		{1234567890, "usd", "12 345 678.90 $"},
		{-1500, "usd", "-15.00 $"}, // refunds render sanely
		{1000, "cdf", "10.00 CDF"}, // unknown currency falls back to the code
	}
	for _, tc := range cases {
		if got := FormatMoney(tc.cents, tc.currency); got != tc.want {
			t.Errorf("FormatMoney(%d, %q) = %q, want %q", tc.cents, tc.currency, got, tc.want)
		}
	}
}
