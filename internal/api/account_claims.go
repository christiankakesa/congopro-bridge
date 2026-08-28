package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/web/templates"
)

// companyForSlug resolves a published company by slug straight from
// Postgres — the source of truth — rather than the engine's in-memory
// cache, so a company created seconds ago is claimable immediately and the
// cache never goes stale for this flow.
func (a *AppEngine) companyForSlug(ctx context.Context, slug string) (id, name string, ok bool) {
	err := a.DB.QueryRow(ctx,
		`SELECT id, name FROM companies WHERE name_seo = $1 AND status = 'published'`, slug,
	).Scan(&id, &name)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Error().Msgf("[account] company lookup %q: %v", slug, err)
		}
		return "", "", false
	}
	return id, name, true
}

// Customer claim flow: a verified customer claims a company; staff arbitrate
// in the admin queue. Everything lives under /account so the session cookie
// never needs to leave its Path scope.

// GET /account/claim?c={slug} — the claim form, or the right state message
// when the company is already claimed / under review.
func (a *AppEngine) AccountClaimFormHandler(w http.ResponseWriter, r *http.Request) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)
	slug := r.URL.Query().Get("c")

	companyID, companyName, ok := a.companyForSlug(r.Context(), slug)
	if !ok {
		a.renderNotFound(w, r)
		return
	}
	state, err := claims.StateForCompany(r.Context(), a.DB, companyID, cust.ID)
	if err != nil {
		log.Error().Msgf("[account] claim state: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AccountClaimPage(nonceFrom(r), cust.Email, slug, companyName, state, "").Render(r.Context(), w)
}

// POST /account/claim — submit the claim, then back to the dashboard where
// it appears in "Mes réclamations".
func (a *AppEngine) AccountClaimSubmitHandler(w http.ResponseWriter, r *http.Request) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)

	renderErr := func(status int, msg string) {
		slug := r.FormValue("company_slug")
		companyID, companyName, ok := a.companyForSlug(r.Context(), slug)
		if !ok {
			http.NotFound(w, r)
			return
		}
		state, err := claims.StateForCompany(r.Context(), a.DB, companyID, cust.ID)
		if err != nil {
			log.Error().Msgf("[account] claim state: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		templates.AccountClaimPage(nonceFrom(r), cust.Email, slug, companyName, state, msg).Render(r.Context(), w)
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	companyID, companyName, ok := a.companyForSlug(r.Context(), r.FormValue("company_slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	claimID, err := claims.Submit(r.Context(), a.DB,
		companyID, cust.ID, cust.Email,
		r.FormValue("phone"), r.FormValue("relationship"), r.FormValue("evidence"))
	if err != nil {
		if claims.IsUserError(err) {
			renderErr(http.StatusUnprocessableEntity, err.Error())
			return
		}
		log.Error().Msgf("[account] submit claim: %v", err)
		renderErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}
	// Staff otherwise only discover claims by visiting /admin/claims. The
	// message carries approve/reject buttons keyed on the new claim id.
	go a.notifyTelegramNewClaim(companyName, cust.Email, baseUrl(r), claimID)
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}
