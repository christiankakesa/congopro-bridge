package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/web/templates"
)

// isHTTPS reports whether the original client request was HTTPS. The app
// itself is always plain HTTP — Traefik terminates TLS in front of it — so
// r.TLS is never set; X-Forwarded-Proto is what Traefik sets instead.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		// Strict, not Lax: this cookie only ever needs to be sent for
		// same-site requests initiated from within /admin itself, and Strict
		// also blocks the cookie on cross-site POSTs — the CSRF defense for
		// every state-changing admin form, without a separate token scheme.
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// AdminLoginFormHandler renders the login form.
func (a *AppEngine) AdminLoginFormHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if _, err := auth.SessionUser(r.Context(), a.DB, cookie.Value); err == nil {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
	}
	nonce, _ := r.Context().Value(constants.NonceKey).(string)
	if err := templates.AdminLoginPage(nonce, "").Render(r.Context(), w); err != nil {
		log.Error().Msgf("[admin] render login page: %v", err)
	}
}

// AdminLoginHandler verifies email + password + TOTP and, on success, issues
// a session cookie. All failure modes render the same generic error message
// regardless of which factor was wrong, to avoid telling an attacker which
// part of their guess was right.
func (a *AppEngine) AdminLoginHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	totpCode := r.FormValue("totp_code")

	user, err := auth.Login(r.Context(), a.DB, email, password, totpCode)
	if err != nil {
		msg := "Email, mot de passe ou code incorrect."
		if errors.Is(err, auth.ErrTOTPNotEnrolled) {
			msg = "Ce compte n'a pas terminé sa configuration. Contactez un administrateur."
		} else if !errors.Is(err, auth.ErrInvalidCredentials) && !errors.Is(err, auth.ErrInvalidTOTPCode) {
			log.Error().Msgf("[admin] login error: %v", err)
		}
		nonce, _ := r.Context().Value(constants.NonceKey).(string)
		w.WriteHeader(http.StatusUnauthorized)
		if rerr := templates.AdminLoginPage(nonce, msg).Render(r.Context(), w); rerr != nil {
			log.Error().Msgf("[admin] render login page: %v", rerr)
		}
		return
	}

	token, expiresAt, err := auth.CreateSession(r.Context(), a.DB, user.ID, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		log.Error().Msgf("[admin] create session: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, r, token, int(time.Until(expiresAt).Seconds()))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *AppEngine) AdminLogoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if err := auth.RevokeSession(r.Context(), a.DB, cookie.Value); err != nil {
			log.Error().Msgf("[admin] revoke session: %v", err)
		}
	}
	setSessionCookie(w, r, "", -1)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// RequireStaffAuth gates a handler behind a valid session cookie, attaching
// the resolved user to the request context. Apply it inside
// WithSecurityHeaders (needs the nonce already set) and outside any
// role-specific check the handler itself wants to add.
func (a *AppEngine) RequireStaffAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		user, err := auth.SessionUser(r.Context(), a.DB, cookie.Value)
		if err != nil {
			setSessionCookie(w, r, "", -1)
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), constants.StaffUserKey, user)
		h(w, r.WithContext(ctx))
	}
}
