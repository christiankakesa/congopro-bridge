package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/api"
	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/promotions"
	"congopro-bridge/internal/telegram"
	"congopro-bridge/internal/web/templates"
)

// Daily staff digest, sent to the Telegram chat by `congopro-bridge -digest`
// under a systemd timer (deploy/systemd/congopro-bridge-digest.timer). A
// oneshot flag mode rather than an in-app ticker: Persistent=true covers
// downtime, a deploy restart can't eat the send, and the server keeps zero
// clock code.

type digestData struct {
	Date             string
	CompaniesAdded   int
	PendingClaims    int
	ActivePromotions int
	MRR              string // preformatted, or "" when Stripe was unreadable
}

// runDigest gathers yesterday's numbers and sends one message. Stripe being
// down degrades the MRR line — it must never fail the digest.
func runDigest(ctx context.Context, pool *pgxpool.Pool, subs api.SubscriptionReader, tg telegram.Notifier) error {
	var d digestData
	d.Date = time.Now().AddDate(0, 0, -1).Format("02/01/2006")

	// Yesterday in server-local days, matching how the team reads "hier".
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM companies
		WHERE created_at >= date_trunc('day', now()) - interval '1 day'
		  AND created_at < date_trunc('day', now())`).Scan(&d.CompaniesAdded); err != nil {
		return fmt.Errorf("digest companies count: %w", err)
	}

	pending, err := claims.CountPending(ctx, pool)
	if err != nil {
		return fmt.Errorf("digest pending claims: %w", err)
	}
	d.PendingClaims = pending

	promos, err := promotions.AllForAdmin(ctx, pool)
	if err != nil {
		return fmt.Errorf("digest promotions: %w", err)
	}
	var subIDs []string
	for _, p := range promos {
		if p.Status == "active" || p.Status == "past_due" {
			d.ActivePromotions++
			if p.StripeSubscriptionID != "" {
				subIDs = append(subIDs, p.StripeSubscriptionID)
			}
		}
	}

	if subs != nil && len(subIDs) > 0 {
		if amounts, err := subs.SubscriptionAmounts(ctx, subIDs); err == nil {
			d.MRR = templates.FormatMoney(api.ComputeMRR(promos, amounts), "usd") + " / mois"
		} else {
			log.Warn().Msgf("[digest] stripe amounts unavailable, MRR omitted: %v", err)
		}
	}

	return tg.Send(ctx, formatDigest(d))
}

// formatDigest is pure so the message shape is pinned by a unit test.
func formatDigest(d digestData) string {
	mrr := d.MRR
	if mrr == "" {
		mrr = "indisponible"
	}
	return fmt.Sprintf(
		"📊 Congopro — bilan du %s\n"+
			"Entreprises ajoutées : %d\n"+
			"Réclamations en attente : %d\n"+
			"Mises en avant actives : %d\n"+
			"MRR : %s",
		d.Date, d.CompaniesAdded, d.PendingClaims, d.ActivePromotions, mrr)
}
