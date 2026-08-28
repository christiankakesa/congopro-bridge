package api

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/ads"
	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/data"
	"congopro-bridge/internal/mail"
	"congopro-bridge/internal/promotions"
	"congopro-bridge/internal/telegram"
	"congopro-bridge/internal/web"
	"congopro-bridge/internal/web/templates"
)

type AppEngine struct {
	Engine *data.Engine
	DB     *pgxpool.Pool
	// Ads is the in-memory serving snapshot of the ads CMS; nil disables
	// ad endpoints entirely (only in tests — main.go always wires one).
	Ads *ads.Store
	// Mailer sends transactional email (customer OTP codes). nil when email
	// is not configured — account features then degrade to a clean 503
	// instead of half-working. MailEnabled mirrors "not nil" for template
	// logic without interface-nil pitfalls.
	Mailer      mail.Mailer
	MailEnabled bool

	// ContactTo is where the public /contact form delivers. Never taken
	// from request input — see internal/api/contact.go.
	ContactTo string

	// Stripe promoted listings. StripeCheckout nil or StripeEnabled false
	// disables the promote endpoints (clean 503).
	StripeCheckout      CheckoutCreator
	StripeEnabled       bool
	StripeWebhookSecret string
	// StripeSubs reads live subscription amounts for /admin/revenue.
	// nil (Stripe disabled or faked out in tests) renders the page with a
	// warning banner and blank amounts instead of failing.
	StripeSubs SubscriptionReader

	// Telegram posts staff notifications (new claims, contact messages,
	// promotion events…) to a private chat. nil disables them silently —
	// notifications are best-effort by design, see notify_telegram.go.
	Telegram telegram.Notifier
	// TelegramBot is the rich side of the same client (inline keyboards,
	// callback answers, message edits) — the bot v2 responder. nil when
	// Telegram is disabled or in v1-only tests; the new-claim notification
	// then falls back to the plain Notifier without buttons.
	TelegramBot TelegramResponder
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type SearchResponse struct {
	Query   string              `json:"query"`
	Results []data.SearchResult `json:"results"`
	Total   int                 `json:"total"`
}

type HealthResponse struct {
	Status    string `json:"status"`
	Companies int    `json:"companies,omitempty"`
}

type AIResponse struct {
	Query  string `json:"query"`
	Answer string `json:"answer"`
}

const defaultTitle = "Congopro | Moteur de recherche boosté à l'IA"

var (
	startupTime = time.Now()
	cssHash     string
)

func init() {
	cssHash = templates.CSSVersion

}

func (a *AppEngine) WithCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := a.Engine.Config.AllowedOrigin

		// Empty AllowedOrigin (the default) means cross-origin access is disabled:
		// no Access-Control-* headers are sent. This never affects the shipped
		// frontend, which only calls the API same-origin — browsers don't gate
		// same-origin requests behind CORS headers at all.
		if origin != "" {
			if origin != "*" {
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (a *AppEngine) WithSecurityHeaders(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce, err := generateNonce()
		if err != nil {
			log.Error().Msgf("[security] nonce generation failed: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Store nonce so the template can use it
		ctx := context.WithValue(r.Context(), constants.NonceKey, nonce)

		csp := "default-src 'self'; " +
			"script-src 'self' 'nonce-" + nonce + "' https://www.googletagmanager.com https://*.google-analytics.com; " +
			"connect-src 'self' " +
			"https://*.google-analytics.com " +
			"https://analytics.google.com " +
			"https://*.analytics.google.com " +
			"https://*.googletagmanager.com " +
			"https://www.google.com " +
			"https://pagead2.googlesyndication.com " +
			"https://stats.g.doubleclick.net; " +
			"style-src 'self' 'unsafe-inline'; " +
			"img-src 'self' data: https: https://*.google-analytics.com https://*.doubleclick.net; " +
			"frame-src 'self' https://*.googletagmanager.com; " +
			"frame-ancestors 'none'"

		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY") // older browser fallback for frame-ancestors
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		h(w, r.WithContext(ctx))
	}
}

func (a *AppEngine) SearchHandler(w http.ResponseWriter, r *http.Request) {
	htmxReq := isHTMXRequest(r)

	select {
	case <-a.Engine.IndexingDone:
	case <-r.Context().Done():
		return
	default:
		if htmxReq {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			templates.SearchResultsFragment("", nil, 0, "server still indexing, please retry", nil).Render(r.Context(), w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "server still indexing, please retry"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var results []data.SearchResult
	var searchErr string
	if q != "" {
		var err error
		results, err = a.Engine.Search(q)
		if err != nil {
			log.Error().Msgf("[search] error: %v", err)
			searchErr = "search failed"
		}
	}
	if results == nil {
		results = []data.SearchResult{}
	}

	// Paid "Mise en avant": promoted companies that match the query are
	// pinned to the top (see pinPromoted). Both the HTML and JSON branches
	// serve the pinned order so API consumers see what the site shows.
	promoted := map[string]bool{}
	if len(results) > 0 {
		promoted = a.promotedSet(r, results)
		results = pinPromoted(results, promoted)
	}

	if htmxReq {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if q != "" {
			// Mirrors the SPA's history.replaceState(null, "", `/?q=${q}`) —
			// the whole debounced-typing session replaces one history entry
			// rather than pushing a new one per keystroke.
			w.Header().Set("HX-Replace-Url", "/?q="+url.QueryEscape(q))
		}
		if searchErr != "" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		templates.SearchResultsFragment(q, results, len(results), searchErr, promoted).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if searchErr != "" {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: searchErr})
		return
	}
	json.NewEncoder(w).Encode(SearchResponse{
		Query:   q,
		Results: results,
		Total:   len(results),
	})
}

func (a *AppEngine) HealthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	select {
	case <-a.Engine.IndexingDone:
		if a.Engine.IndexingError() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(HealthResponse{
				Status:    "degraded",
				Companies: len(a.Engine.Companies()),
			})
			return
		}
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "ready",
			Companies: len(a.Engine.Companies()),
		})
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(HealthResponse{Status: "indexing"})
	}
}

func (a *AppEngine) AIAnswerHandler(w http.ResponseWriter, r *http.Request) {
	htmxReq := isHTMXRequest(r)

	writeErr := func(status int, msg string) {
		if htmxReq {
			// htmx doesn't swap 4xx/5xx responses by default — the client
			// resets the trigger button via the htmx:responseError event
			// instead, so the body here is just for debugging.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			w.Write([]byte(msg))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
	}

	select {
	case <-a.Engine.IndexingDone:
	case <-r.Context().Done():
		return
	default:
		writeErr(http.StatusServiceUnavailable, "server still indexing, please retry")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(http.StatusBadRequest, "empty query")
		return
	}

	results, err := a.Engine.Search(q)
	if err != nil {
		writeErr(http.StatusInternalServerError, "search error")
		return
	}

	answer, err := a.Engine.GenerateAnswer(q, results)
	if err != nil {
		log.Error().Msgf("[ai] Ollama error: %v", err)
		writeErr(http.StatusBadGateway, "AI service is unavailable")
		return
	}

	if htmxReq {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		templates.AIAnswerFragment(answer).Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AIResponse{
		Query:  q,
		Answer: answer,
	})
}

// SitemapHandler serves /sitemap.xml — plain XML, compressed on the wire by
// Traefik like every other text response. This is the URL robots.txt
// advertises and the one to submit in Search Console.
func (a *AppEngine) SitemapHandler(w http.ResponseWriter, r *http.Request) {
	a.Engine.SitemapMu.RLock()
	data := a.Engine.SitemapCache
	a.Engine.SitemapMu.RUnlock()

	if len(data) == 0 {
		http.Error(w, "Not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400") // 1 day
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// SitemapGzHandler serves /sitemap.xml.gz. The gzip bytes ARE the resource
// here (a real .gz download, per the sitemap spec), not transport encoding —
// the old handler labelled them Content-Encoding: gzip, so intermediaries
// transparently decompressed and the URL delivered plain XML under a .gz
// name. Kept alive because crawlers already know this URL from robots.txt.
func (a *AppEngine) SitemapGzHandler(w http.ResponseWriter, r *http.Request) {
	a.Engine.SitemapMu.RLock()
	data := a.Engine.SitemapGzCache
	a.Engine.SitemapMu.RUnlock()

	if len(data) == 0 {
		http.Error(w, "Not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400") // 1 day
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (a *AppEngine) ServeSPAHandler(w http.ResponseWriter, r *http.Request) {
	a.serveSPA(w, r, defaultTitle)
}

func (a *AppEngine) HelpHandler(w http.ResponseWriter, r *http.Request) {
	a.serveStaticPage(w, r, "Aide | Congopro", "Comment chercher une entreprise, réclamer votre fiche ou mettre en avant votre société sur Congopro : le guide d'utilisation complet.", "help", "Aide et Assistance")
}

func (a *AppEngine) PrivacyHandler(w http.ResponseWriter, r *http.Request) {
	a.serveStaticPage(w, r, "Confidentialité | Congopro", "Politique de confidentialité de Congopro : quelles données sont collectées, pourquoi, et ce que nous ne faisons jamais avec.", "privacy", "Politique de Confidentialité")
}

func (a *AppEngine) TermsHandler(w http.ResponseWriter, r *http.Request) {
	a.serveStaticPage(w, r, "Conditions d'utilisation | Congopro", "Conditions d'utilisation du moteur de recherche et de l'annuaire professionnel Congopro.", "terms", "Conditions d'Utilisation")
}

// serveStaticPage renders a fully server-rendered content page (help,
// privacy, terms) — real content in the initial response, no client-side
// fetch-and-inject round trip, and no SPA JS bundle required to see it.
func (a *AppEngine) serveStaticPage(w http.ResponseWriter, r *http.Request, title, description, page, heading string) {
	content, err := web.ContentFS.ReadFile("content/" + page + ".html")
	if err != nil {
		a.renderNotFound(w, r)
		return
	}

	nonce, _ := r.Context().Value(constants.NonceKey).(string)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour

	if err := templates.StaticPage(title, description, canonicalURL(r), nonce, cssHash, heading, string(content)).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[templates] render static page %q: %v", page, err)
	}
}

// CompanyHandler renders a company's profile as a real, fully server-rendered
// page instead of the old approach of serving the generic SPA shell and
// letting client JS resolve the company via a "-company-slug:" search hack.
func (a *AppEngine) CompanyHandler(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/company/")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	select {
	case <-a.Engine.IndexingDone:
	case <-r.Context().Done():
		return
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("server still indexing, please retry"))
		return
	}

	company, err := a.Engine.FindBySlug(slug)
	if err != nil {
		a.renderNotFound(w, r)
		return
	}

	nonce, _ := r.Context().Value(constants.NonceKey).(string)
	title := company.Name + " | Congopro"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300") // 5 minutes

	promoted, err := promotions.IsPromoted(r.Context(), a.DB, company.ID)
	if err != nil {
		log.Error().Msgf("[company] promoted lookup: %v", err)
	}
	verified, err := claims.IsClaimed(r.Context(), a.DB, company.ID)
	if err != nil {
		log.Error().Msgf("[company] claim lookup: %v", err)
	}
	if err := templates.CompanyPage(title, canonicalURL(r), nonce, cssHash, company, promoted, verified).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[templates] render company page %q: %v", slug, err)
	}
}

func (a *AppEngine) ContentHandler(w http.ResponseWriter, r *http.Request) {
	page := strings.TrimPrefix(r.URL.Path, "/api/v1/content/")
	content, err := web.ContentFS.ReadFile("content/" + page + ".html")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600") // 1 hour

	w.Write(content)
}

func (a *AppEngine) AdsHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	now := time.Now()

	st := a.Ads.Settings()
	eAds := a.Ads.EligibleAds(q, now)

	if eAds == nil {
		eAds = []ads.AdWire{}
	} else if len(eAds) > 1 {
		rand.Shuffle(len(eAds), func(i, j int) {
			eAds[i], eAds[j] = eAds[j], eAds[i]
		})
	}

	// 75% of the time show 1 AD, 25% of the time show the configured MaxPerPage
	maxAdsPerPage := st.MaxPerPage
	if maxAdsPerPage > 1 {
		if rand.Intn(100) < 75 { // 75% (0 to 74)
			maxAdsPerPage = 1
		}
	}

	w.Header().Set("Cache-Control", "no-cache") // needed

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if !st.Active {
			if q == "" {
				templates.HomepageAdFragment(nil).Render(r.Context(), w)
			} else {
				templates.AdsResultsFragment(q, nil, st.RotationSec).Render(r.Context(), w)
			}
			return
		}
		if q == "" {
			var homeAd *ads.AdWire
			if len(eAds) > 0 {
				homeAd = &eAds[0]
			}
			templates.HomepageAdFragment(homeAd).Render(r.Context(), w)
			return
		}
		slots := templates.SelectAdSlots(eAds, maxAdsPerPage)
		templates.AdsResultsFragment(q, slots, st.RotationSec).Render(r.Context(), w)
		return
	}

	resp := ads.AdResponse{
		Active:      st.Active,
		RotationSec: st.RotationSec,
		MaxPerPage:  maxAdsPerPage,
		Ads:         eAds,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *AppEngine) AdsPreviewDataHandler(w http.ResponseWriter, r *http.Request) {
	previews := ads.GetAdPreviews()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(previews)
}

func (a *AppEngine) AdsPreviewPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	nonce, _ := r.Context().Value(constants.NonceKey).(string)

	if err := templates.AdsPreviewPage(canonicalURL(r), nonce, cssHash, ads.GetAdPreviews()).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[templates] render ads preview page: %v", err)
	}
}

var (
	langSubscriptionsPathRegex = regexp.MustCompile(`^(/(fr|en))?/subscriptions/?$`)
	langCompanyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/company/([^/]+)/?$`)
	langHelpPathRegex          = regexp.MustCompile(`^(/(fr|en))?/(about|contact|faq|help)/?$`)
	langPrivacyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/privacy/?$`)
	langTermsPathRegex         = regexp.MustCompile(`^(/(fr|en))?/terms/?$`)
)

func (a *AppEngine) FrontendHandler(w http.ResponseWriter, r *http.Request) {
	if q := r.URL.Query(); q.Has("page") {
		target := "/"
		if clean := strings.TrimSpace(q.Get("q")); clean != "" {
			target = "/?q=" + clean
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	if r.URL.Path == "/index.html" || r.URL.Path == "/index.htm" ||
		r.URL.Path == "/fr" || r.URL.Path == "/fr/" ||
		r.URL.Path == "/en" || r.URL.Path == "/en/" ||
		langSubscriptionsPathRegex.MatchString(r.URL.Path) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}

	if matches := langCompanyPathRegex.FindStringSubmatch(r.URL.Path); matches != nil {
		companySlug := matches[3]
		http.Redirect(w, r, "/company/"+companySlug, http.StatusPermanentRedirect)
		return
	}
	if langHelpPathRegex.MatchString(r.URL.Path) {
		http.Redirect(w, r, "/help", http.StatusPermanentRedirect)
		return
	}
	if langPrivacyPathRegex.MatchString(r.URL.Path) {
		http.Redirect(w, r, "/privacy", http.StatusPermanentRedirect)
		return
	}
	if langTermsPathRegex.MatchString(r.URL.Path) {
		http.Redirect(w, r, "/terms", http.StatusPermanentRedirect)
		return
	}

	if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/company/") && r.URL.Path != "/help" && r.URL.Path != "/privacy" && r.URL.Path != "/terms" {
		a.renderNotFound(w, r)
		return
	}

	a.ServeSPAHandler(w, r)
}

func (a *AppEngine) serveSPA(w http.ResponseWriter, r *http.Request, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	nonce, _ := r.Context().Value(constants.NonceKey).(string)

	if err := templates.HomePage(title, canonicalURL(r), nonce, cssHash, len(a.Engine.Companies())).Render(r.Context(), w); err != nil {
		log.Error().Err(err).Msg("render home page")
	}
}

// renderNotFound serves the branded 404 for HTML page routes. Asset and API
// endpoints deliberately keep http.NotFound's plain response — an HTML page
// is the wrong answer to a missing font or a JSON call.
func (a *AppEngine) renderNotFound(w http.ResponseWriter, r *http.Request) {
	nonce, _ := r.Context().Value(constants.NonceKey).(string)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	if err := templates.NotFoundPage(canonicalURL(r), nonce, cssHash).Render(r.Context(), w); err != nil {
		log.Error().Msgf("[templates] render 404 page: %v", err)
	}
}

func FaviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
	http.ServeContent(w, r, "favicon.ico", startupTime, bytes.NewReader(web.FaviconICO))
}

func RobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// One hour, not the old year+immutable: robots.txt is a policy file, and
	// a year of caching means a crawling mistake takes a year to retract.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(web.RobotsTXT)))
	w.Write(web.RobotsTXT)
}

func ServeManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days
	http.ServeContent(w, r, "site.webmanifest", startupTime, bytes.NewReader(web.SiteManifest))
}

func FontsHandler(w http.ResponseWriter, r *http.Request) {
	fileName := strings.TrimPrefix(r.URL.Path, "/fonts/")
	fileName = path.Clean(fileName)
	if fileName == "" || fileName == "." || fileName == "/" {
		http.NotFound(w, r)
		return
	}

	f, err := web.FontsFS.Open("fonts/" + fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		http.NotFound(w, r)
		return
	}

	readSeeker, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		readSeeker = bytes.NewReader(data)
	}

	contentType := "font/woff2"
	switch ext := strings.ToLower(path.Ext(fileName)); ext {
	case ".woff2":
		contentType = "font/woff2"
	case ".woff":
		contentType = "font/woff"
	case ".ttf":
		contentType = "font/ttf"
	case ".otf":
		contentType = "font/otf"
	case ".eot":
		contentType = "application/vnd.ms-fontobject"
	case ".svg":
		contentType = "image/svg+xml" // SVG fonts can be served as image/svg+xml
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // 1 year

	http.ServeContent(w, r, fileName, stat.ModTime(), readSeeker)
}

func ImagesHandler(w http.ResponseWriter, r *http.Request) {
	fileName := strings.TrimPrefix(r.URL.Path, "/images/")
	fileName = path.Clean(fileName)
	if fileName == "" || fileName == "." || fileName == "/" {
		http.NotFound(w, r)
		return
	}

	f, err := web.ImagesFS.Open("images/" + fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	modTime := stat.ModTime()

	readSeeker, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		readSeeker = bytes.NewReader(data)
	}

	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days
	http.ServeContent(w, r, fileName, modTime, readSeeker)
}

func TailwindCssHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "style.min.css", startupTime, bytes.NewReader(web.TailwindCSS))
}

func HtmxJSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "htmx.min.js", startupTime, bytes.NewReader(web.HtmxJS))
}

func AppJSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "app.js", startupTime, bytes.NewReader(web.AppJS))
}

func PreloadJSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "preload.min.js", startupTime, bytes.NewReader(web.PreloadJS))
}

// isHTMXRequest reports whether r was issued by htmx (as opposed to a
// programmatic JSON API consumer). Used to content-negotiate between the
// existing JSON contract and a server-rendered HTML fragment from the same
// route, without duplicating business logic across two handlers.
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// canonicalURL builds the page's canonical address on the apex domain — the
// host the sitemap, robots.txt and Traefik's www→apex redirect all agree on.
// (It used to say www.congopro.com while the sitemap said congopro.com, which
// fed Google two contradictory host signals for every page.)
func canonicalURL(r *http.Request) string {
	const host = "https://congopro.com"

	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	return host + path
}
