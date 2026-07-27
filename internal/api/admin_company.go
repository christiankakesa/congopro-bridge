package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/web/templates"
)

func staffUser(r *http.Request) *auth.User {
	u, _ := r.Context().Value(constants.StaffUserKey).(*auth.User)
	return u
}

func nonceFrom(r *http.Request) string {
	nonce, _ := r.Context().Value(constants.NonceKey).(string)
	return nonce
}

const adminPageSize = 50

func (a *AppEngine) AdminCompaniesListHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * adminPageSize

	rows, err := a.DB.Query(r.Context(), `
		SELECT id, name, city, country, status, updated_at
		FROM companies
		WHERE $1 = '' OR name ILIKE '%' || $1 || '%'
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3
	`, q, adminPageSize+1, offset)
	if err != nil {
		log.Error().Msgf("[admin] list companies: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []templates.AdminCompanyRow
	for rows.Next() {
		var row templates.AdminCompanyRow
		var updatedAt time.Time
		if err := rows.Scan(&row.ID, &row.Name, &row.City, &row.Country, &row.Status, &updatedAt); err != nil {
			log.Error().Msgf("[admin] scan company row: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		row.UpdatedAt = updatedAt.Format("2006-01-02 15:04")
		list = append(list, row)
	}
	if err := rows.Err(); err != nil {
		log.Error().Msgf("[admin] iterate companies: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	hasNext := len(list) > adminPageSize
	if hasNext {
		list = list[:adminPageSize]
	}

	if err := templates.AdminCompaniesList(nonceFrom(r), staffUser(r).Name, q, list, page, hasNext).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[admin] render companies list: %v", err)
	}
}

func (a *AppEngine) AdminCompanyNewFormHandler(w http.ResponseWriter, r *http.Request) {
	form := templates.AdminCompanyFormData{Status: "draft"}
	if err := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, form, false, "").Render(r.Context(), w); err != nil {
		log.Error().Msgf("[admin] render new company form: %v", err)
	}
}

func newCompanyID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// companyFormInput parses and validates the fields shared by create and update.
type companyFormInput struct {
	form templates.AdminCompanyFormData
	lon  any
	lat  any
}

var validStatuses = map[string]bool{"draft": true, "published": true, "disputed": true}

func parseCompanyForm(r *http.Request) (companyFormInput, string) {
	form := templates.AdminCompanyFormData{
		Name:         strings.TrimSpace(r.FormValue("name")),
		NameSeo:      strings.TrimSpace(r.FormValue("name_seo")),
		Activity:     strings.TrimSpace(r.FormValue("activity")),
		City:         strings.TrimSpace(r.FormValue("city")),
		Country:      strings.TrimSpace(r.FormValue("country")),
		Description:  strings.TrimSpace(r.FormValue("description")),
		Slogan:       strings.TrimSpace(r.FormValue("slogan")),
		Website:      strings.TrimSpace(r.FormValue("website")),
		Email:        strings.TrimSpace(r.FormValue("email")),
		Phone:        strings.TrimSpace(r.FormValue("phone")),
		AddressLine1: strings.TrimSpace(r.FormValue("address_line_1")),
		AddressLine2: strings.TrimSpace(r.FormValue("address_line_2")),
		Twitter:      strings.TrimSpace(r.FormValue("twitter")),
		Facebook:     strings.TrimSpace(r.FormValue("facebook")),
		LinkedIn:     strings.TrimSpace(r.FormValue("linkedin")),
		Instagram:    strings.TrimSpace(r.FormValue("instagram")),
		TikTok:       strings.TrimSpace(r.FormValue("tiktok")),
		Whatsapp:     strings.TrimSpace(r.FormValue("whatsapp")),
		Youtube:      strings.TrimSpace(r.FormValue("youtube")),
		StatsShow:    r.FormValue("stats_show") != "",
		Status:       r.FormValue("status"),
		Lon:          strings.TrimSpace(r.FormValue("lon")),
		Lat:          strings.TrimSpace(r.FormValue("lat")),
	}

	if form.Name == "" {
		return companyFormInput{form: form}, "Le nom est obligatoire."
	}
	if !validStatuses[form.Status] {
		return companyFormInput{form: form}, "Statut invalide."
	}

	var lon, lat any
	if form.Lon != "" || form.Lat != "" {
		lonF, errLon := strconv.ParseFloat(form.Lon, 64)
		latF, errLat := strconv.ParseFloat(form.Lat, 64)
		if errLon != nil || errLat != nil {
			return companyFormInput{form: form}, "Longitude/latitude invalides — laissez les deux champs vides ou remplissez les deux."
		}
		lon, lat = lonF, latF
	}

	return companyFormInput{form: form, lon: lon, lat: lat}, ""
}

func (a *AppEngine) AdminCompanyCreateHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	input, errMsg := parseCompanyForm(r)
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, input.form, false, errMsg).Render(r.Context(), w); err != nil {
			log.Error().Msgf("[admin] render new company form: %v", err)
		}
		return
	}

	id, err := newCompanyID()
	if err != nil {
		log.Error().Msgf("[admin] generate company id: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	_, err = a.DB.Exec(r.Context(), `
		INSERT INTO companies (
			id, name, name_seo, activity, city, country, description, slogan,
			website, email, phone, address_line_1, address_line_2, twitter,
			facebook, linkedin, instagram, tiktok, whatsapp, youtube,
			stats_show, status, location
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22,
			CASE WHEN $23::float8 IS NULL THEN NULL
			     ELSE ST_SetSRID(ST_MakePoint($23::float8, $24::float8), 4326)::geography END
		)
	`,
		id, input.form.Name, input.form.NameSeo, input.form.Activity, input.form.City, input.form.Country,
		input.form.Description, input.form.Slogan, input.form.Website, input.form.Email, input.form.Phone,
		input.form.AddressLine1, input.form.AddressLine2, input.form.Twitter, input.form.Facebook,
		input.form.LinkedIn, input.form.Instagram, input.form.TikTok, input.form.Whatsapp, input.form.Youtube,
		boolToInt(input.form.StatsShow), input.form.Status, input.lon, input.lat,
	)
	if err != nil {
		log.Error().Msgf("[admin] insert company: %v", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		if rerr := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, input.form, false, "Erreur lors de l'enregistrement — vérifiez le slug (doit être unique).").Render(r.Context(), w); rerr != nil {
			log.Error().Msgf("[admin] render new company form: %v", rerr)
		}
		return
	}

	a.reloadEngineAsync()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *AppEngine) AdminCompanyEditFormHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	form, err := a.loadCompanyForm(r, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		log.Error().Msgf("[admin] load company %s: %v", id, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, *form, true, "").Render(r.Context(), w); err != nil {
		log.Error().Msgf("[admin] render edit company form: %v", err)
	}
}

func (a *AppEngine) loadCompanyForm(r *http.Request, id string) (*templates.AdminCompanyFormData, error) {
	var form templates.AdminCompanyFormData
	var statsShow int
	var lon, lat *float64
	err := a.DB.QueryRow(r.Context(), `
		SELECT id, name, name_seo, activity, city, country, description, slogan,
		       website, email, phone, address_line_1, address_line_2, twitter,
		       facebook, linkedin, instagram, tiktok, whatsapp, youtube,
		       stats_show, status, ST_X(location::geometry), ST_Y(location::geometry)
		FROM companies WHERE id = $1
	`, id).Scan(
		&form.ID, &form.Name, &form.NameSeo, &form.Activity, &form.City, &form.Country,
		&form.Description, &form.Slogan, &form.Website, &form.Email, &form.Phone,
		&form.AddressLine1, &form.AddressLine2, &form.Twitter, &form.Facebook,
		&form.LinkedIn, &form.Instagram, &form.TikTok, &form.Whatsapp, &form.Youtube,
		&statsShow, &form.Status, &lon, &lat,
	)
	if err != nil {
		return nil, err
	}
	form.StatsShow = statsShow != 0
	if lon != nil {
		form.Lon = strconv.FormatFloat(*lon, 'f', -1, 64)
	}
	if lat != nil {
		form.Lat = strconv.FormatFloat(*lat, 'f', -1, 64)
	}
	return &form, nil
}

func (a *AppEngine) AdminCompanyUpdateHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	input, errMsg := parseCompanyForm(r)
	input.form.ID = id
	if errMsg != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, input.form, true, errMsg).Render(r.Context(), w); err != nil {
			log.Error().Msgf("[admin] render edit company form: %v", err)
		}
		return
	}

	tag, err := a.DB.Exec(r.Context(), `
		UPDATE companies SET
			name = $2, name_seo = $3, activity = $4, city = $5, country = $6,
			description = $7, slogan = $8, website = $9, email = $10, phone = $11,
			address_line_1 = $12, address_line_2 = $13, twitter = $14, facebook = $15,
			linkedin = $16, instagram = $17, tiktok = $18, whatsapp = $19, youtube = $20,
			stats_show = $21, status = $22,
			location = CASE WHEN $23::float8 IS NULL THEN NULL
			                ELSE ST_SetSRID(ST_MakePoint($23::float8, $24::float8), 4326)::geography END,
			updated_at = now()
		WHERE id = $1
	`,
		id, input.form.Name, input.form.NameSeo, input.form.Activity, input.form.City, input.form.Country,
		input.form.Description, input.form.Slogan, input.form.Website, input.form.Email, input.form.Phone,
		input.form.AddressLine1, input.form.AddressLine2, input.form.Twitter, input.form.Facebook,
		input.form.LinkedIn, input.form.Instagram, input.form.TikTok, input.form.Whatsapp, input.form.Youtube,
		boolToInt(input.form.StatsShow), input.form.Status, input.lon, input.lat,
	)
	if err != nil {
		log.Error().Msgf("[admin] update company %s: %v", id, err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		if rerr := templates.AdminCompanyForm(nonceFrom(r), staffUser(r).Name, input.form, true, "Erreur lors de l'enregistrement — vérifiez le slug (doit être unique).").Render(r.Context(), w); rerr != nil {
			log.Error().Msgf("[admin] render edit company form: %v", rerr)
		}
		return
	}
	if tag.RowsAffected() == 0 {
		http.NotFound(w, r)
		return
	}

	a.reloadEngineAsync()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// reloadEngineAsync re-syncs the public search index after an admin write
// without blocking the request — Meilisearch's background embedding step can
// take a while under load, far longer than an HTTP handler should hold open.
func (a *AppEngine) reloadEngineAsync() {
	go func() {
		if err := a.Engine.Reload(); err != nil {
			log.Error().Msgf("[admin] engine reload after write failed: %v", err)
		}
	}()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
