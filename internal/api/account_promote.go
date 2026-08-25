package api

import (
	"net/http"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/data"
	"congopro-bridge/internal/promotions"
	"congopro-bridge/internal/web/templates"
)

// Promoted-listing flow: a customer with an approved claim subscribes via
// Stripe Checkout to promote their company. The webhook
// (StripeWebhookHandler) is the lifecycle source of truth; these handlers
// only open checkouts and present state.

// GET /account/promote — the offer, eligible companies, current promotions.
func (a *AppEngine) AccountPromotePageHandler(w http.ResponseWriter, r *http.Request) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)

	eligible, current, priceStr := a.promotePageData(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AccountPromotePage(nonceFrom(r), cust.Email,
		eligible, current, priceStr,
		r.URL.Query().Get("ok") == "1", r.URL.Query().Get("annule") == "1",
	).Render(r.Context(), w)
}

func (a *AppEngine) promotePageData(r *http.Request) (eligible []promotions.EligibleCompany, current []promotions.Promotion, priceStr string) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)
	var err error
	eligible, err = promotions.EligibleForCustomer(r.Context(), a.DB, cust.ID)
	if err != nil {
		log.Error().Msgf("[promote] eligible: %v", err)
	}
	current, err = promotions.ForCustomer(r.Context(), a.DB, cust.ID)
	if err != nil {
		log.Error().Msgf("[promote] current: %v", err)
	}
	if a.StripeCheckout != nil {
		if p, ok := a.StripeCheckout.PriceDisplay(r.Context()); ok {
			priceStr = FormatPrice(p)
		}
	}
	return eligible, current, priceStr
}

// POST /account/promote — ownership-gated checkout opening.
func (a *AppEngine) AccountPromoteCheckoutHandler(w http.ResponseWriter, r *http.Request) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)

	renderErr := func(status int, msg string) {
		eligible, current, priceStr := a.promotePageData(r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		templates.AccountPromotePage(nonceFrom(r), cust.Email, eligible, current, priceStr, false, false).
			Render(r.Context(), w)
	}

	if a.StripeCheckout == nil || !a.StripeEnabled {
		renderErr(http.StatusServiceUnavailable, "La mise en avant n'est pas disponible pour le moment.")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	slug := r.FormValue("company_slug")

	companyID, companyName, ok := a.companyForSlug(r.Context(), slug)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Ownership: only the approved claimant may promote.
	state, err := claims.StateForCompany(r.Context(), a.DB, companyID, cust.ID)
	if err != nil {
		log.Error().Msgf("[promote] claim state: %v", err)
		renderErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}
	if !state.ClaimedByMe {
		renderErr(http.StatusUnprocessableEntity,
			"Vous devez d'abord faire approuver la réclamation de « "+companyName+" » avant de la promouvoir.")
		return
	}

	promo, err := promotions.CreatePending(r.Context(), a.DB, companyID, cust.ID, "")
	if err != nil {
		if err == promotions.ErrAlreadyPromoted {
			renderErr(http.StatusUnprocessableEntity, promotions.ErrAlreadyPromoted.Error())
			return
		}
		log.Error().Msgf("[promote] create pending: %v", err)
		renderErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}

	base := baseUrl(r)
	sessionID, sessionURL, err := a.StripeCheckout.CreatePromotionSession(r.Context(),
		promo.ID, cust.Email,
		base+"/account/promote?ok=1", base+"/account/promote?annule=1")
	if err != nil {
		// Free the slot immediately — an abandoned pending row would lock
		// the company for 24h otherwise.
		promotions.CancelPending(r.Context(), a.DB, promo.ID)
		log.Error().Msgf("[promote] checkout session: %v", err)
		renderErr(http.StatusBadGateway, "La connexion à Stripe a échoué. Réessayez dans un instant.")
		return
	}
	if err := promotions.SetSessionID(r.Context(), a.DB, promo.ID, sessionID); err != nil {
		log.Error().Msgf("[promote] set session id: %v", err)
	}

	http.Redirect(w, r, sessionURL, http.StatusSeeOther)
}

func baseUrl(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// promotedSet batches the badge lookup for one rendered page of results.
func (a *AppEngine) promotedSet(r *http.Request, results []data.SearchResult) map[string]bool {
	ids := make([]string, 0, len(results))
	for _, res := range results {
		ids = append(ids, res.Company.ID)
	}
	set, err := promotions.PromotedCompanyIDs(r.Context(), a.DB, ids)
	if err != nil {
		log.Error().Msgf("[search] promoted lookup: %v", err)
		return map[string]bool{}
	}
	return set
}
