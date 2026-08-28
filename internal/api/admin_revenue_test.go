package api

import (
	"testing"

	"congopro-bridge/internal/promotions"
)

func promo(status, subID string) promotions.Promotion {
	return promotions.Promotion{Status: status, StripeSubscriptionID: subID}
}

func TestComputeMRR(t *testing.T) {
	amounts := map[string]SubAmount{
		"sub_m":  {Amount: 1500, Currency: "usd", Interval: "month"},
		"sub_m2": {Amount: 1000, Currency: "usd", Interval: "month"},
		"sub_y":  {Amount: 12000, Currency: "usd", Interval: "year"},
	}
	cases := []struct {
		name   string
		promos []promotions.Promotion
		want   int64
	}{
		{"empty", nil, 0},
		{"one monthly", []promotions.Promotion{promo("active", "sub_m")}, 1500},
		{"two monthly", []promotions.Promotion{promo("active", "sub_m"), promo("active", "sub_m2")}, 2500},
		// Revenue at risk is not revenue.
		{"past_due excluded", []promotions.Promotion{promo("active", "sub_m"), promo("past_due", "sub_m2")}, 1500},
		{"canceled excluded", []promotions.Promotion{promo("canceled", "sub_m")}, 0},
		{"pending excluded", []promotions.Promotion{promo("pending", "sub_m")}, 0},
		{"yearly divided by 12", []promotions.Promotion{promo("active", "sub_y")}, 1000},
		// Stripe couldn't be read for this sub — skip, don't guess.
		{"missing amount skipped", []promotions.Promotion{promo("active", "sub_unknown"), promo("active", "sub_m")}, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ComputeMRR(tc.promos, amounts); got != tc.want {
				t.Fatalf("ComputeMRR = %d, want %d", got, tc.want)
			}
		})
	}
}
