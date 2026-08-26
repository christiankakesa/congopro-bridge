package api

import (
	"net/http"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/web/templates"
)

const dashboardPendingPreview = 4

// AdminDashboardHandler renders the staff landing page: company/claims/ads
// counts and the newest pending claims. Legacy companies-list URLs
// (/admin?q=…&page=…) permanently redirect to /admin/companies.
func (a *AppEngine) AdminDashboardHandler(w http.ResponseWriter, r *http.Request) {
	if q := r.URL.Query(); q.Has("q") || q.Has("page") {
		http.Redirect(w, r, "/admin/companies?"+q.Encode(), http.StatusMovedPermanently)
		return
	}

	var stats templates.AdminDashboardStats

	rows, err := a.DB.Query(r.Context(), `SELECT status, count(*) FROM companies GROUP BY status`)
	if err != nil {
		log.Error().Msgf("[admin] dashboard companies counts: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			log.Error().Msgf("[admin] dashboard scan company count: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		stats.CompaniesTotal += n
		switch status {
		case "published":
			stats.CompaniesPublished = n
		case "draft":
			stats.CompaniesDraft = n
		case "disputed":
			stats.CompaniesDisputed = n
		}
	}
	rows.Close()

	rows, err = a.DB.Query(r.Context(), `SELECT status, count(*) FROM ads GROUP BY status`)
	if err != nil {
		log.Error().Msgf("[admin] dashboard ads counts: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			log.Error().Msgf("[admin] dashboard scan ad count: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		stats.AdsTotal += n
		if status == "active" {
			stats.AdsActive = n
		}
	}
	rows.Close()

	stats.AdsSystemOn = a.Ads.Settings().Active

	pending, err := claims.ListForAdmin(r.Context(), a.DB, "pending")
	if err != nil {
		log.Error().Msgf("[admin] dashboard pending claims: %v", err)
	} else if len(pending) > dashboardPendingPreview {
		pending = pending[:dashboardPendingPreview]
	}
	stats.RecentPending = pending

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.AdminDashboard(nonceFrom(r), a.adminNav(r), stats).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[admin] render dashboard: %v", err)
	}
}
