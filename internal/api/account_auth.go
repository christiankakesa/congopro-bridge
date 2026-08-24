package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/mail"
	"congopro-bridge/internal/web/templates"
)

// Customer account flows: passwordless email-OTP login under /account.
// Conventions mirror admin auth (SameSite=Strict cookie as the CSRF defense,
// PRG redirects, generic French error messages) — see admin_auth.go.

// setCustomerSessionCookie writes or clears the customer session cookie.
// Path is /account only; SameSite=Strict blocks it on every cross-site
// request, which is the CSRF strategy (same contract as the admin cookie).
func setCustomerSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     customers.CustomerSessionCookieName,
		Value:    token,
		Path:     "/account",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// customerFromCookie resolves the current session, if any. A nil customer
// with nil error must not happen; callers check err.
func (a *AppEngine) customerFromCookie(r *http.Request) (*customers.Customer, string, error) {
	c, err := r.Cookie(customers.CustomerSessionCookieName)
	if err != nil {
		return nil, "", customers.ErrSessionNotFound
	}
	cust, err := customers.SessionCustomer(r.Context(), a.DB, c.Value)
	return cust, c.Value, err
}

// safeNext validates a post-login redirect target: site-relative only,
// never protocol-relative (open redirect), never external.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && len(next) <= 512 {
		return next
	}
	return ""
}

// GET /account/login — step 1: email form. ?next= survives into the verify
// step so a claim link from a company page flows through login and back.
func (a *AppEngine) AccountLoginPageHandler(w http.ResponseWriter, r *http.Request) {
	if cust, _, err := a.customerFromCookie(r); err == nil && cust != nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AccountLoginPage(nonceFrom(r), "", safeNext(r.URL.Query().Get("next"))).Render(r.Context(), w)
}

// POST /account/login — request a code for the submitted email.
func (a *AppEngine) AccountRequestCodeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.MailEnabled || a.Mailer == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		templates.AccountLoginPage(nonceFrom(r), "La connexion par email n'est pas disponible pour le moment.", "").
			Render(r.Context(), w)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	email, err := customers.NormalizeEmail(r.FormValue("email"))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		templates.AccountLoginPage(nonceFrom(r), err.Error(), safeNext(r.FormValue("next"))).Render(r.Context(), w)
		return
	}

	next := safeNext(r.FormValue("next"))
	code, err := customers.IssueCode(r.Context(), a.DB, email)
	if err != nil {
		if err == customers.ErrCooldown {
			// Not an error for the user: their code is already in flight.
			http.Redirect(w, r, verifyURL(email, ""), http.StatusSeeOther)
			return
		}
		log.Error().Msgf("[account] issue code: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		templates.AccountLoginPage(nonceFrom(r), "Une erreur est survenue. Réessayez dans un instant.", "").
			Render(r.Context(), w)
		return
	}

	if err := a.Mailer.Send(mail.Message{
		To:      email,
		Subject: "Votre code de connexion — Congopro",
		Body: fmt.Sprintf(
			"Bonjour,\n\n"+
				"Votre code de connexion Congopro est :\n\n"+
				"    %s\n\n"+
				"Il est valable %d minutes. Si vous n'êtes pas à l'origine de cette demande, ignorez simplement cet email.\n\n"+
				"L'équipe Congopro — https://congopro.com\n",
			code, int(customers.CodeTTL.Minutes()),
		),
	}); err != nil {
		log.Error().Msgf("[account] send otp mail: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		templates.AccountLoginPage(nonceFrom(r), "L'envoi de l'email a échoué. Réessayez dans un instant.", "").
			Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, verifyURL(email, next), http.StatusSeeOther)
}

// GET /account/verify?e=… — step 2: code form.
func (a *AppEngine) AccountVerifyPageHandler(w http.ResponseWriter, r *http.Request) {
	email, err := customers.NormalizeEmail(r.URL.Query().Get("e"))
	if err != nil {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AccountVerifyPage(nonceFrom(r), email, "", safeNext(r.URL.Query().Get("next"))).Render(r.Context(), w)
}

// POST /account/verify — check the code, create the account on first login,
// open the session.
func (a *AppEngine) AccountVerifyCodeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	email, err := customers.NormalizeEmail(r.FormValue("email"))
	if err != nil {
		http.Redirect(w, r, "/account/login", http.StatusSeeOther)
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))

	renderVerifyErr := func(status int, msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		templates.AccountVerifyPage(nonceFrom(r), email, msg, "").Render(r.Context(), w)
	}

	if err := customers.VerifyCode(r.Context(), a.DB, email, code); err != nil {
		if err == customers.ErrInvalidCode {
			renderVerifyErr(http.StatusUnauthorized, customers.ErrInvalidCode.Error())
			return
		}
		log.Error().Msgf("[account] verify code: %v", err)
		renderVerifyErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}

	cust, err := customers.CreateOrGetByEmail(r.Context(), a.DB, email)
	if err != nil {
		if err == customers.ErrCustomerDisabled {
			renderVerifyErr(http.StatusForbidden, "Ce compte est désactivé.")
			return
		}
		log.Error().Msgf("[account] create or get customer: %v", err)
		renderVerifyErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}

	token, expiresAt, err := customers.CreateSession(r.Context(), a.DB, cust.ID, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		log.Error().Msgf("[account] create session: %v", err)
		renderVerifyErr(http.StatusInternalServerError, "Une erreur est survenue. Réessayez dans un instant.")
		return
	}
	customers.TouchLogin(r.Context(), a.DB, cust.ID)

	setCustomerSessionCookie(w, r, token, int(time.Until(expiresAt).Seconds()))
	if next := safeNext(r.FormValue("next")); next != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// POST /account/logout
func (a *AppEngine) AccountLogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(customers.CustomerSessionCookieName); err == nil {
		if err := customers.RevokeSession(r.Context(), a.DB, c.Value); err != nil {
			log.Error().Msgf("[account] revoke session: %v", err)
		}
	}
	setCustomerSessionCookie(w, r, "", -1)
	http.Redirect(w, r, "/account/login", http.StatusSeeOther)
}

// RequireCustomerAuth gates /account pages: valid session required, the
// customer is attached to the request context.
func (a *AppEngine) RequireCustomerAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cust, _, err := a.customerFromCookie(r)
		if err != nil || cust == nil {
			// Clear any stale cookie so the next login starts clean.
			setCustomerSessionCookie(w, r, "", -1)
			http.Redirect(w, r, "/account/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), constants.CustomerUserKey, cust)
		h(w, r.WithContext(ctx))
	}
}

// GET /account — the dashboard: identity plus "Mes réclamations".
func (a *AppEngine) AccountDashboardHandler(w http.ResponseWriter, r *http.Request) {
	cust, _ := r.Context().Value(constants.CustomerUserKey).(*customers.Customer)
	myClaims, err := claims.ListByCustomer(r.Context(), a.DB, cust.ID)
	if err != nil {
		log.Error().Msgf("[account] list claims: %v", err)
		myClaims = nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AccountDashboard(nonceFrom(r), cust, myClaims).Render(r.Context(), w)
}

func verifyURL(email, next string) string {
	u := "/account/verify?e=" + url.QueryEscape(email)
	if next != "" {
		u += "&next=" + url.QueryEscape(next)
	}
	return u
}
