package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/ads"
	"congopro-bridge/internal/web/templates"
)

// Ads CMS admin: campaign CRUD + global settings. Every successful write
// triggers an async Store.Reload — a reload must never block or fail the
// admin action (same contract as the search engine's post-write reload).

var adIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

// AdminAdRow is one row of the campaign list.
type AdminAdRow struct {
	ID          string
	Title       string
	Placement   string
	Status      string
	Period      string
	Weight      int
	SoldByEmail string
	UpdatedAt   string
}

// StaffOption backs the sold-by select.
type StaffOption struct {
	ID    string
	Label string
}

// AdminAdFormData backs the new/edit forms.
type AdminAdFormData struct {
	ID            string
	Title         string
	Description   string
	URL           string
	DisplayURL    string
	Label         string
	Color         string
	PeriodStart   string
	PeriodEnd     string
	Weight        string
	Placement     string
	Keywords      string // one per line
	Status        string
	SoldByUserID  string
	CustomerEmail string
	Price         string
	Currency      string
}

// GET /admin/ads?status=
func (a *AppEngine) AdminAdsListHandler(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "draft" && status != "active" && status != "paused" && status != "expired" {
		status = ""
	}
	rows, err := a.DB.Query(r.Context(), `
		SELECT a.id, a.title, a.placement, a.status, a.weight, a.updated_at,
		       COALESCE(a.period_start::text, ''), COALESCE(a.period_end::text, ''),
		       COALESCE(u.email, '')
		FROM ads a LEFT JOIN users u ON u.id = a.sold_by_user_id
		WHERE ($1 = '' OR a.status = $1)
		ORDER BY (a.status = 'active') DESC, a.updated_at DESC
		LIMIT 200`, status)
	if err != nil {
		log.Error().Msgf("[admin] list ads: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []templates.AdminAdRow
	for rows.Next() {
		var row templates.AdminAdRow
		var start, end string
		var updatedAt time.Time
		if err := rows.Scan(&row.ID, &row.Title, &row.Placement, &row.Status, &row.Weight,
			&updatedAt, &start, &end, &row.SoldByEmail); err != nil {
			log.Error().Msgf("[admin] scan ad row: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		row.Period = adPeriodLabel(start, end)
		row.UpdatedAt = updatedAt.Format("02/01/2006")
		list = append(list, row)
	}

	settings := a.Ads.Settings()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AdminAdsList(nonceFrom(r), a.adminNav(r), status, list, settings).Render(r.Context(), w)
}

func adPeriodLabel(start, end string) string {
	if start == "" && end == "" {
		return "Illimitée"
	}
	if end == "" {
		return "Dès " + start
	}
	if start == "" {
		return "Jusqu'au " + end
	}
	return start + " → " + end
}

// POST /admin/ads/settings — the no-redeploy kill switch.
func (a *AppEngine) AdminAdsSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	active := r.FormValue("active") == "on"
	rotation, err := strconv.Atoi(strings.TrimSpace(r.FormValue("rotation_sec")))
	if err != nil || rotation <= 0 {
		http.Error(w, "rotation invalide", http.StatusUnprocessableEntity)
		return
	}
	maxPerPage, err := strconv.Atoi(strings.TrimSpace(r.FormValue("max_per_page")))
	if err != nil || maxPerPage < 1 || maxPerPage > 3 {
		http.Error(w, "max par page invalide (1–3)", http.StatusUnprocessableEntity)
		return
	}

	if _, err := a.DB.Exec(r.Context(), `
		INSERT INTO ads_settings (id, active, rotation_sec, max_per_page)
		VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET active=$1, rotation_sec=$2, max_per_page=$3`,
		active, rotation, maxPerPage); err != nil {
		log.Error().Msgf("[admin] save ads settings: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	a.reloadAdsAsync()
	http.Redirect(w, r, "/admin/ads?flash=settings", http.StatusSeeOther)
}

// GET /admin/ads/new
func (a *AppEngine) AdminAdNewFormHandler(w http.ResponseWriter, r *http.Request) {
	form := templates.AdminAdFormData{Status: "draft", Weight: "1", Currency: "USD", Placement: "search_results"}
	a.renderAdForm(w, r, form, true, "")
}

// GET /admin/ads/{id}/edit
func (a *AppEngine) AdminAdEditFormHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var f templates.AdminAdFormData
	var keywords []string
	err := a.DB.QueryRow(r.Context(), `
		SELECT id, title, description, url, display_url, label, color,
		       COALESCE(period_start::text, ''), COALESCE(period_end::text, ''),
		       weight::text, placement, status,
		       COALESCE(sold_by_user_id::text, ''), COALESCE(customer_id::text, ''),
		       COALESCE(price_cents::text, ''), currency, keywords
		FROM ads WHERE id = $1`, id,
	).Scan(&f.ID, &f.Title, &f.Description, &f.URL, &f.DisplayURL, &f.Label, &f.Color,
		&f.PeriodStart, &f.PeriodEnd, &f.Weight, &f.Placement, &f.Status,
		&f.SoldByUserID, &f.CustomerEmail, &f.Price, &f.Currency, &keywords)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if f.CustomerEmail != "" {
		var email string
		if err := a.DB.QueryRow(r.Context(),
			`SELECT email FROM customers WHERE id::text = $1`, f.CustomerEmail).Scan(&email); err == nil {
			f.CustomerEmail = email
		}
	}
	f.Keywords = strings.Join(keywords, "\n")
	a.renderAdForm(w, r, f, false, "")
}

// POST /admin/ads/new
func (a *AppEngine) AdminAdCreateHandler(w http.ResponseWriter, r *http.Request) {
	a.adUpsert(w, r, true)
}

// POST /admin/ads/{id}/edit
func (a *AppEngine) AdminAdUpdateHandler(w http.ResponseWriter, r *http.Request) {
	a.adUpsert(w, r, false)
}

func (a *AppEngine) adUpsert(w http.ResponseWriter, r *http.Request, isNew bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var f templates.AdminAdFormData
	f.ID = strings.TrimSpace(r.FormValue("id"))
	f.Title = strings.TrimSpace(r.FormValue("title"))
	f.Description = strings.TrimSpace(r.FormValue("description"))
	f.URL = strings.TrimSpace(r.FormValue("url"))
	f.DisplayURL = strings.TrimSpace(r.FormValue("display_url"))
	f.Label = strings.TrimSpace(r.FormValue("label"))
	f.Color = strings.TrimSpace(r.FormValue("color"))
	f.PeriodStart = strings.TrimSpace(r.FormValue("period_start"))
	f.PeriodEnd = strings.TrimSpace(r.FormValue("period_end"))
	f.Weight = strings.TrimSpace(r.FormValue("weight"))
	f.Placement = strings.TrimSpace(r.FormValue("placement"))
	f.Keywords = strings.TrimSpace(r.FormValue("keywords"))
	f.Status = r.FormValue("status")
	f.SoldByUserID = r.FormValue("sold_by")
	f.CustomerEmail = strings.TrimSpace(r.FormValue("customer_email"))
	f.Price = strings.TrimSpace(r.FormValue("price"))
	f.Currency = strings.TrimSpace(strings.ToUpper(r.FormValue("currency")))
	if f.Currency == "" {
		f.Currency = "USD"
	}

	renderErr := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		a.renderAdForm(w, r, f, isNew, msg)
	}

	// Validation — French, field by field.
	if isNew && !adIDPattern.MatchString(f.ID) {
		renderErr("Identifiant invalide : minuscules, chiffres et tirets (3–64 caractères).")
		return
	}
	if f.Title == "" {
		renderErr("Le titre est requis.")
		return
	}
	if !strings.HasPrefix(f.URL, "https://") {
		renderErr("L'URL de destination doit commencer par https://")
		return
	}
	for _, d := range []struct{ name, val string }{{"de début", f.PeriodStart}, {"de fin", f.PeriodEnd}} {
		if d.val != "" {
			if _, err := time.Parse("2006-01-02", d.val); err != nil {
				renderErr("Date " + d.name + " invalide (format AAAA-MM-JJ).")
				return
			}
		}
	}
	weight, err := strconv.Atoi(f.Weight)
	if err != nil || weight < 0 || weight > 100 {
		renderErr("Le poids doit être un entier entre 0 et 100.")
		return
	}
	switch f.Placement {
	case "", "homepage", "search_results":
	default:
		renderErr("Emplacement invalide.")
		return
	}
	switch f.Status {
	case "draft", "active", "paused", "expired":
	default:
		renderErr("Statut invalide.")
		return
	}
	var priceCents *int
	if f.Price != "" {
		parts := strings.Split(f.Price, ".")
		if len(parts) > 2 || len(parts[0]) == 0 || !allDigits(parts[0]) || (len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 2 || !allDigits(parts[1]))) {
			renderErr("Prix invalide (format : 150 ou 150.00).")
			return
		}
		cents, _ := strconv.Atoi(parts[0])
		cents *= 100
		if len(parts) == 2 {
			frac, _ := strconv.Atoi(parts[1])
			if len(parts[1]) == 1 {
				frac *= 10
			}
			cents += frac
		}
		priceCents = &cents
	}

	// Sales attribution resolution.
	var customerID *string
	if f.CustomerEmail != "" {
		var id string
		if err := a.DB.QueryRow(r.Context(),
			`SELECT id::text FROM customers WHERE lower(email) = lower($1)`, f.CustomerEmail).Scan(&id); err != nil {
			renderErr("Aucun compte client avec cet email — le client doit d'abord se connecter une fois.")
			return
		}
		customerID = &id
	}
	keywords := splitKeywords(f.Keywords)

	if isNew {
		_, err := a.DB.Exec(r.Context(), `
			INSERT INTO ads (id, title, description, url, display_url, label, color,
			                 period_start, period_end, weight, placement, keywords, status,
			                 sold_by_user_id, customer_id, price_cents, currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
			        NULLIF($8,'')::date, NULLIF($9,'')::date,
			        $10,$11,$12,$13,
			        NULLIF($14,'')::uuid, $15, $16, $17)`,
			f.ID, f.Title, f.Description, f.URL, f.DisplayURL, f.Label, f.Color,
			f.PeriodStart, f.PeriodEnd, weight, f.Placement, keywords, f.Status,
			f.SoldByUserID, customerID, priceCents, f.Currency)
		if err != nil {
			if strings.Contains(err.Error(), "ads_pkey") {
				renderErr("Un identifiant de campagne identique existe déjà.")
				return
			}
			log.Error().Msgf("[admin] create ad: %v", err)
			renderErr("Une erreur est survenue. Réessayez.")
			return
		}
	} else {
		tag, err := a.DB.Exec(r.Context(), `
			UPDATE ads SET title=$2, description=$3, url=$4, display_url=$5, label=$6, color=$7,
			               period_start=NULLIF($8,'')::date, period_end=NULLIF($9,'')::date,
			               weight=$10, placement=$11, keywords=$12, status=$13,
			               sold_by_user_id=NULLIF($14,'')::uuid, customer_id=$15,
			               price_cents=$16, currency=$17, updated_at=now()
			WHERE id = $1`,
			r.PathValue("id"), f.Title, f.Description, f.URL, f.DisplayURL, f.Label, f.Color,
			f.PeriodStart, f.PeriodEnd, weight, f.Placement, keywords, f.Status,
			f.SoldByUserID, customerID, priceCents, f.Currency)
		if err != nil {
			log.Error().Msgf("[admin] update ad: %v", err)
			renderErr("Une erreur est survenue. Réessayez.")
			return
		}
		if tag.RowsAffected() == 0 {
			http.NotFound(w, r)
			return
		}
	}

	a.reloadAdsAsync()
	flash := "saved"
	if isNew {
		flash = "created"
	}
	http.Redirect(w, r, "/admin/ads?flash="+flash, http.StatusSeeOther)
}

func (a *AppEngine) renderAdForm(w http.ResponseWriter, r *http.Request, form templates.AdminAdFormData, isNew bool, errorMsg string) {
	staff, err := a.staffOptions(r)
	if err != nil {
		log.Error().Msgf("[admin] staff options: %v", err)
	}
	labelValues := make([]string, 0, len(ads.LabelPresets))
	for _, p := range ads.LabelPresets {
		labelValues = append(labelValues, p.Label)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.AdminAdForm(nonceFrom(r), a.adminNav(r), form, isNew, errorMsg, staff, labelValues).Render(r.Context(), w)
}

func (a *AppEngine) staffOptions(r *http.Request) ([]templates.StaffOption, error) {
	rows, err := a.DB.Query(r.Context(), `SELECT id::text, email FROM users WHERE status = 'active' ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []templates.StaffOption
	for rows.Next() {
		var o templates.StaffOption
		if err := rows.Scan(&o.ID, &o.Label); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func staffDisplayName(r *http.Request) string {
	if u := staffUser(r); u != nil {
		if u.Name != "" {
			return u.Name
		}
		return u.Email
	}
	return ""
}

func splitKeywords(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// reloadAdsAsync refreshes the serving snapshot after a write; failures are
// logged, never surfaced to the admin action that triggered them.
func (a *AppEngine) reloadAdsAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.Ads.Reload(ctx, a.DB); err != nil {
			log.Error().Msgf("[admin] ads reload after write: %v", err)
		}
	}()
}
