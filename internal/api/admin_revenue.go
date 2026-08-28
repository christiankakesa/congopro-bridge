package api

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/promotions"
	"congopro-bridge/internal/web/templates"
)

// stripeReadTimeout bounds the live Stripe calls on the revenue page — an
// admin page render must never hang on an external API.
const stripeReadTimeout = 5 * time.Second

// AdminRevenueHandler renders /admin/revenue: promotion subscriptions with
// live amounts from Stripe (the local table stores no money on purpose) and
// the ads sales ledger from the ads table. Stripe being down degrades to a
// warning banner over local data — never a failed page.
func (a *AppEngine) AdminRevenueHandler(w http.ResponseWriter, r *http.Request) {
	promos, err := promotions.AllForAdmin(r.Context(), a.DB)
	if err != nil {
		log.Error().Msgf("[admin] revenue promotions: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var subIDs []string
	for _, p := range promos {
		if (p.Status == "active" || p.Status == "past_due") && p.StripeSubscriptionID != "" {
			subIDs = append(subIDs, p.StripeSubscriptionID)
		}
	}

	amounts := map[string]SubAmount{}
	stripeWarn := a.StripeSubs == nil
	if a.StripeSubs != nil && len(subIDs) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), stripeReadTimeout)
		amounts, err = a.StripeSubs.SubscriptionAmounts(ctx, subIDs)
		cancel()
		if err != nil {
			log.Error().Msgf("[admin] revenue stripe amounts: %v", err)
			amounts = map[string]SubAmount{}
			stripeWarn = true
		}
	}

	data := templates.AdminRevenueData{
		StripeWarn: stripeWarn,
		MRRCents:   ComputeMRR(promos, amounts),
	}
	for _, p := range promos {
		if p.Status == "active" || p.Status == "past_due" {
			data.ActiveCount++
		}
		row := templates.RevenuePromotion{
			CompanyName:   p.CompanyName,
			CompanySlug:   p.CompanyNameSeo,
			CustomerEmail: p.CustomerEmail,
			Status:        p.Status,
			CreatedAt:     p.CreatedAt.Format("02/01/2006"),
		}
		if p.CurrentPeriodEnd != nil {
			row.PeriodEnd = p.CurrentPeriodEnd.Format("02/01/2006")
		}
		if amt, ok := amounts[p.StripeSubscriptionID]; ok {
			row.Amount = templates.FormatMoney(amt.Amount, amt.Currency) + intervalSuffix(amt.Interval)
		}
		data.Promotions = append(data.Promotions, row)
	}

	if err := a.loadAdsRevenue(r.Context(), &data); err != nil {
		log.Error().Msgf("[admin] revenue ads: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AdminRevenue(nonceFrom(r), a.adminNav(r), data).Render(r.Context(), w)
}

func intervalSuffix(interval string) string {
	if interval == "year" {
		return " / an"
	}
	return " / mois"
}

// loadAdsRevenue reads priced ads straight from the table — the ads Store
// is a serving snapshot for delivery, not a sales ledger.
func (a *AppEngine) loadAdsRevenue(ctx context.Context, data *templates.AdminRevenueData) error {
	rows, err := a.DB.Query(ctx, `
		SELECT a.title, a.status, a.price_cents, a.currency,
		       COALESCE(cu.email, ''), COALESCE(u.name, ''), a.created_at
		FROM ads a
		LEFT JOIN customers cu ON cu.id = a.customer_id
		LEFT JOIN users u ON u.id = a.sold_by_user_id
		WHERE a.price_cents IS NOT NULL
		ORDER BY a.created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			ad         templates.RevenueAd
			priceCents int64
			currency   string
			createdAt  time.Time
		)
		if err := rows.Scan(&ad.Title, &ad.Status, &priceCents, &currency,
			&ad.CustomerEmail, &ad.SoldBy, &createdAt); err != nil {
			return err
		}
		ad.Price = templates.FormatMoney(priceCents, currency)
		ad.CreatedAt = createdAt.Format("02/01/2006")
		data.Ads = append(data.Ads, ad)
		data.AdsTotalCents += priceCents
		if ad.Status == "active" {
			data.AdsActiveCents += priceCents
		}
	}
	return rows.Err()
}
