package api

import (
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/web/templates"
)

// adminNav assembles the per-request admin chrome state: user identity and
// role, the pending-claims badge (one COUNT per page render — fine at staff
// scale), the active nav item derived from the path, and the one-shot
// ?flash= toast code (whitelisted in templates.adminFlashMessage).
func (a *AppEngine) adminNav(r *http.Request) templates.AdminNav {
	nav := templates.AdminNav{}
	if u := staffUser(r); u != nil {
		nav.UserName = u.Name
		if nav.UserName == "" {
			nav.UserName = u.Email
		}
		nav.Role = u.Role
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/admin/companies"):
		nav.Active = "companies"
	case strings.HasPrefix(r.URL.Path, "/admin/claims"):
		nav.Active = "claims"
	case strings.HasPrefix(r.URL.Path, "/admin/ads"):
		nav.Active = "ads"
	default:
		nav.Active = "dashboard"
	}
	switch f := r.URL.Query().Get("flash"); f {
	case "created", "saved", "approved", "rejected", "settings":
		nav.Flash = f
	}
	if nav.UserName != "" {
		if n, err := claims.CountPending(r.Context(), a.DB); err == nil {
			nav.PendingClaims = n
		} else {
			log.Error().Msgf("[admin] count pending claims: %v", err)
		}
	}
	return nav
}
