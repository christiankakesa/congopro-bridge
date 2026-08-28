package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	stripeg "github.com/stripe/stripe-go/v82"
	stripesub "github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/webhook"

	"congopro-bridge/internal/promotions"
)

// StripeWebhookHandler receives Stripe lifecycle events and applies them to
// the promotions state. Signature verification uses the RAW body — this
// handler must never be wrapped in anything that parses the request first.
// DB failures return 5xx so Stripe retries; replays are safe (appliers are
// idempotent).
func (a *AppEngine) StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	const maxBody = 1 << 20 // 1 MiB, far above any Stripe event
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// IgnoreAPIVersionMismatch: the dashboard webhook endpoint may be
	// pinned to a different Stripe API version than stripe-go expects; we
	// read only a few stable scalar fields, so refuse nothing over version
	// drift (signature verification still enforced).
	event, err := webhook.ConstructEventWithOptions(body, r.Header.Get("Stripe-Signature"),
		a.StripeWebhookSecret, webhook.ConstructEventOptions{
			Tolerance:                webhookTolerance,
			IgnoreAPIVersionMismatch: true,
		})
	if err != nil {
		log.Warn().Msgf("[stripe] webhook signature rejected: %v", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	var dbErr error
	switch event.Type {
	case stripeg.EventTypeCheckoutSessionCompleted:
		dbErr = a.applyCheckoutCompleted(r.Context(), event.Data.Raw)
	case stripeg.EventTypeCustomerSubscriptionUpdated:
		dbErr = a.applySubscriptionUpdated(r.Context(), event.Data.Raw)
	case stripeg.EventTypeCustomerSubscriptionDeleted:
		dbErr = a.applySubscriptionDeleted(r.Context(), event.Data.Raw)
	default:
		// Unsubscribed event types are acked without action.
	}

	if dbErr != nil {
		log.Error().Msgf("[stripe] webhook %s apply: %v", event.Type, dbErr)
		// 5xx → Stripe retries; appliers are idempotent.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *AppEngine) applyCheckoutCompleted(ctx context.Context, raw json.RawMessage) error {
	var session stripeg.CheckoutSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return err
	}
	subID := ""
	if session.Subscription != nil {
		subID = session.Subscription.ID
	}
	cusID := ""
	if session.Customer != nil {
		cusID = session.Customer.ID
	}
	// The checkout event carries the subscription id but not its period
	// end — one retrieve gives the authoritative first period. Since API
	// v82 the period lives on the (single) subscription item. Best-effort:
	// a failed retrieve still activates (the subscription.updated event
	// that follows carries the period), and tests can run without Stripe.
	periodEnd := time.Time{}
	if subID != "" && stripeg.Key != "" { // no key (tests) → no network call
		if sub, err := stripesub.Get(subID, nil); err == nil {
			if n := subscriptionPeriodEnd(sub); n != 0 {
				periodEnd = time.Unix(n, 0)
			}
		} else {
			log.Warn().Msgf("[stripe] subscription retrieve during checkout (%s) failed, period end left to subscription.updated: %v", subID, err)
		}
	}
	activated, err := promotions.ApplyCheckoutCompleted(ctx, a.DB,
		session.ID, cusID, subID, periodEnd)
	if err == nil && activated {
		// Fire-and-forget, strictly after the DB write succeeded: nothing
		// in the notify path may ever influence the webhook's status code
		// (5xx makes Stripe retry). Replays affect zero rows → no re-notify.
		if name := a.companyNameForPromotionSession(session.ID); name != "" {
			go a.notifyTelegram(msgPromotionActivated(name))
		}
	}
	return err
}

func (a *AppEngine) applySubscriptionUpdated(ctx context.Context, raw json.RawMessage) error {
	var sub stripeg.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return err
	}
	oldStatus, newStatus, err := promotions.ApplySubscriptionUpdated(ctx, a.DB,
		sub.ID, string(sub.Status), time.Unix(subscriptionPeriodEnd(&sub), 0))
	// Only the transition INTO past_due is worth waking staff for — the
	// activation and cancellation stories have their own events.
	if err == nil && oldStatus != newStatus && newStatus == "past_due" {
		if name := a.companyNameForSubscription(sub.ID); name != "" {
			go a.notifyTelegram(msgPromotionPastDue(name))
		}
	}
	return err
}

func (a *AppEngine) applySubscriptionDeleted(ctx context.Context, raw json.RawMessage) error {
	var sub stripeg.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return err
	}
	canceled, err := promotions.ApplySubscriptionDeleted(ctx, a.DB, sub.ID)
	if err == nil && canceled {
		if name := a.companyNameForSubscription(sub.ID); name != "" {
			go a.notifyTelegram(msgPromotionCanceled(name))
		}
	}
	return err
}

// companyNameForPromotionSession resolves the company behind a checkout
// session for notification text. Best-effort: "" on any failure, and the
// caller then skips the notification rather than sending a nameless one.
func (a *AppEngine) companyNameForPromotionSession(sessionID string) string {
	var name string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.DB.QueryRow(ctx, `
		SELECT co.name FROM promotions p JOIN companies co ON co.id = p.company_id
		WHERE p.stripe_session_id = $1`, sessionID).Scan(&name); err != nil {
		log.Warn().Msgf("[telegram] company lookup for session %s: %v", sessionID, err)
	}
	return name
}

func (a *AppEngine) companyNameForSubscription(subID string) string {
	var name string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.DB.QueryRow(ctx, `
		SELECT co.name FROM promotions p JOIN companies co ON co.id = p.company_id
		WHERE p.stripe_subscription_id = $1`, subID).Scan(&name); err != nil {
		log.Warn().Msgf("[telegram] company lookup for subscription %s: %v", subID, err)
	}
	return name
}

// subscriptionPeriodEnd reads current_period_end from the first
// subscription item (its home since stripe-go v82); 0 when absent.
func subscriptionPeriodEnd(sub *stripeg.Subscription) int64 {
	if sub == nil || len(sub.Items.Data) == 0 {
		return 0
	}
	return sub.Items.Data[0].CurrentPeriodEnd
}
