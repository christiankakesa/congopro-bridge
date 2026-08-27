package config

import "testing"

// A product id pasted into STRIPE_PRICE_ID is the easy mistake: the dashboard
// shows "ID du produit" prominently on the product page, while the price id
// lives in the Pricing table below it. It must fail at boot, not at a
// customer's checkout.
func TestValidateStripe_RejectsProductIDInPriceSlot(t *testing.T) {
	c := &Config{
		StripeSecretKey:     "sk_live_abc123",
		StripeWebhookSecret: "whsec_abc123",
		StripePriceID:       "prod_V9MbuUYgN3CgFX",
	}
	err := c.ValidateStripe()
	if err == nil {
		t.Fatal("expected a product id in STRIPE_PRICE_ID to be rejected")
	}
	if got := err.Error(); !contains(got, "price_") || !contains(got, "prod_") {
		t.Errorf("error should name the expected prefix and show the offending value: %s", got)
	}
	if contains(err.Error(), "V9MbuUYgN3CgFX") {
		t.Error("full id should be redacted in the error")
	}
}

func TestValidateStripe_AcceptsCorrectIDs(t *testing.T) {
	c := &Config{
		StripeSecretKey:     "sk_live_abc123",
		StripeWebhookSecret: "whsec_abc123",
		StripePriceID:       "price_1U90qCPoP8YyyKAn",
	}
	if err := c.ValidateStripe(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateStripe_UnsetStaysValid(t *testing.T) {
	if err := (&Config{}).ValidateStripe(); err != nil {
		t.Fatalf("Stripe disabled should stay valid: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
