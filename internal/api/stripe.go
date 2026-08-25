package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	stripeg "github.com/stripe/stripe-go/v82"
	stripecheckout "github.com/stripe/stripe-go/v82/checkout/session"
	stripeprice "github.com/stripe/stripe-go/v82/price"
)

// Stripe plumbing for promoted listings. The service is wired into
// AppEngine when config validates; nil disables the promote endpoints.

// CheckoutCreator creates a Stripe Checkout Session for a promotion. An
// interface so handler tests can fake it — no Stripe account needed.
type CheckoutCreator interface {
	// CreatePromotionSession returns the hosted-checkout URL for the given
	// promotion. The returned id must be the Stripe session id (used to
	// match the checkout.session.completed webhook).
	CreatePromotionSession(ctx context.Context, promotionID, customerEmail, successURL, cancelURL string) (sessionID, sessionURL string, err error)
	// PriceDisplay presents the plan on the promote page (cached).
	PriceDisplay(ctx context.Context) (PriceDisplay, bool)
}

// PriceDisplay is the cached plan presentation for the promote page.
type PriceDisplay struct {
	Amount   int64  // in the currency's smallest unit (cents)
	Currency string // lowercase ISO code
	Interval string // "month" | "year"
}

type stripeService struct {
	apiKey  string
	priceID string

	mu          sync.Mutex
	priceOnce   bool
	priceCached PriceDisplay
}

// NewStripeService builds the real CheckoutCreator for AppEngine wiring.
func NewStripeService(apiKey, priceID string) CheckoutCreator {
	stripeg.Key = apiKey
	return &stripeService{apiKey: apiKey, priceID: priceID}
}

var _ CheckoutCreator = (*stripeService)(nil)

func (s *stripeService) CreatePromotionSession(ctx context.Context, promotionID, customerEmail, successURL, cancelURL string) (string, string, error) {
	params := &stripeg.CheckoutSessionParams{
		Mode: stripeg.String(string(stripeg.CheckoutSessionModeSubscription)),
		LineItems: []*stripeg.CheckoutSessionLineItemParams{
			{Price: stripeg.String(s.priceID), Quantity: stripeg.Int64(1)},
		},
		ClientReferenceID: stripeg.String(promotionID),
		CustomerEmail:     stripeg.String(customerEmail),
		SuccessURL:        stripeg.String(successURL),
		CancelURL:         stripeg.String(cancelURL),
		Metadata:          map[string]string{"promotion_id": promotionID},
	}
	session, err := stripecheckout.New(params)
	if err != nil {
		return "", "", fmt.Errorf("stripe checkout session: %w", err)
	}
	return session.ID, session.URL, nil
}

// PriceDisplay caches the plan presentation after the first successful
// fetch — the promote page shouldn't call Stripe on every render.
func (s *stripeService) PriceDisplay(ctx context.Context) (PriceDisplay, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.priceOnce {
		return s.priceCached, true
	}
	price, err := stripeprice.Get(s.priceID, nil)
	if err != nil {
		log.Error().Msgf("[stripe] price retrieve %s: %v", s.priceID, err)
		return PriceDisplay{}, false
	}
	display := PriceDisplay{Currency: string(price.Currency), Amount: price.UnitAmount}
	if price.Recurring != nil && price.Recurring.Interval != "" {
		display.Interval = string(price.Recurring.Interval)
	}
	s.priceCached = display
	s.priceOnce = true
	return display, true
}

// FormatPrice renders a PriceDisplay for the French UI: "10.00 $ / mois".
// Always two decimals — standard money display (a trimmed "15.5 €" looks
// wrong next to what Stripe will charge).
func FormatPrice(p PriceDisplay) string {
	amount := fmt.Sprintf("%.2f", float64(p.Amount)/100)
	currency := map[string]string{"usd": "$", "eur": "€"}[p.Currency]
	if currency == "" {
		currency = p.Currency
	}
	interval := "/ mois"
	if p.Interval == "year" {
		interval = "/ an"
	}
	return amount + " " + currency + " " + interval
}

// webhookTolerance matches Stripe's recommendation for replay windows.
const webhookTolerance = 5 * time.Minute
