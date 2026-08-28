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
	stripesub "github.com/stripe/stripe-go/v82/subscription"

	"congopro-bridge/internal/promotions"
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

// SubAmount is one subscription's recurring amount, live from Stripe.
// Prices are immutable once used, so subscribers created at different
// times can be on different amounts — the local promotions table stores
// none of this on purpose (Stripe stays the source of truth for money).
type SubAmount struct {
	Amount   int64  // cents per interval
	Currency string // lowercase ISO code
	Interval string // "month" | "year"
}

// SubscriptionReader fetches live amounts for the revenue page and the
// daily digest. Separate from CheckoutCreator so existing fakes keep
// compiling and tests can fake just the read side.
type SubscriptionReader interface {
	SubscriptionAmounts(ctx context.Context, subIDs []string) (map[string]SubAmount, error)
}

// StripeService is what NewStripeService returns: checkout plus read side.
type StripeService interface {
	CheckoutCreator
	SubscriptionReader
}

type stripeService struct {
	apiKey  string
	priceID string

	mu          sync.Mutex
	priceOnce   bool
	priceCached PriceDisplay
	subsCached  map[string]SubAmount
	subsFetched time.Time
}

// NewStripeService builds the real Stripe service for AppEngine wiring.
func NewStripeService(apiKey, priceID string) StripeService {
	stripeg.Key = apiKey
	return &stripeService{apiKey: apiKey, priceID: priceID}
}

var _ StripeService = (*stripeService)(nil)

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

// subsCacheTTL keeps admin page reloads from hammering Stripe; one minute
// of staleness on a revenue readout costs nothing.
const subsCacheTTL = time.Minute

// SubscriptionAmounts fetches each subscription's recurring amount. Per-ID
// Get is fine at this scale (a handful of live promotions); all-or-nothing
// on error keeps the caller's degradation story simple (banner + blank
// amounts, never a half-filled table).
func (s *stripeService) SubscriptionAmounts(ctx context.Context, subIDs []string) (map[string]SubAmount, error) {
	s.mu.Lock()
	if s.subsCached != nil && time.Since(s.subsFetched) < subsCacheTTL && coversAll(s.subsCached, subIDs) {
		cached := s.subsCached
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	out := make(map[string]SubAmount, len(subIDs))
	for _, id := range subIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sub, err := stripesub.Get(id, nil)
		if err != nil {
			return nil, fmt.Errorf("stripe subscription %s: %w", id, err)
		}
		if len(sub.Items.Data) == 0 || sub.Items.Data[0].Price == nil {
			continue // defensive: a subscription without a priced item has no amount to show
		}
		item := sub.Items.Data[0]
		amt := SubAmount{
			Amount:   item.Price.UnitAmount * max(item.Quantity, 1),
			Currency: string(item.Price.Currency),
		}
		if item.Price.Recurring != nil {
			amt.Interval = string(item.Price.Recurring.Interval)
		}
		out[id] = amt
	}

	s.mu.Lock()
	s.subsCached = out
	s.subsFetched = time.Now()
	s.mu.Unlock()
	return out, nil
}

func coversAll(cache map[string]SubAmount, ids []string) bool {
	for _, id := range ids {
		if _, ok := cache[id]; !ok {
			return false
		}
	}
	return true
}

// ComputeMRR sums monthly-equivalent cents over ACTIVE promotions with a
// known amount; yearly intervals divide by 12 (integer cents). past_due is
// excluded — revenue at risk is not revenue.
func ComputeMRR(promos []promotions.Promotion, amounts map[string]SubAmount) int64 {
	var mrr int64
	for _, p := range promos {
		if p.Status != "active" {
			continue
		}
		amt, ok := amounts[p.StripeSubscriptionID]
		if !ok {
			continue
		}
		switch amt.Interval {
		case "year":
			mrr += amt.Amount / 12
		default: // "month" and anything unrecognized counts as-is
			mrr += amt.Amount
		}
	}
	return mrr
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
