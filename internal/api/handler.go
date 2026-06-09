package api

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math/rand"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/ads"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/data"
	"congopro-bridge/internal/web"
)

type AppEngine struct {
	Engine *data.Engine
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
	startupTime    = time.Now()
	cssHash        string
	indexTmpl      *template.Template
	adsPreviewTmpl *template.Template
)

func init() {
	cssHash = fmt.Sprintf("%.8x", md5.Sum(web.TailwindCSS))
	indexTmpl = template.Must(template.New("index").Parse(string(web.IndexHTML)))
	adsPreviewTmpl = template.Must(template.New("ads-preview").Parse(string(web.AdsPreviewHTML)))
}

func (a *AppEngine) WithCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.Engine.Config.AllowedOrigin != "*" {
			w.Header().Add("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Origin", a.Engine.Config.AllowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func generateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func (a *AppEngine) WithSecurityHeaders(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce := generateNonce()

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
	w.Header().Set("Content-Type", "application/json")

	select {
	case <-a.Engine.IndexingDone:
	case <-r.Context().Done():
		return
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "server still indexing, please retry"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		json.NewEncoder(w).Encode(SearchResponse{
			Query:   q,
			Results: []data.SearchResult{},
			Total:   0,
		})
		return
	}

	results, err := a.Engine.Search(q)
	if err != nil {
		log.Error().Msgf("[search] error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "search failed"})
		return
	}

	if results == nil {
		results = []data.SearchResult{}
	}

	json.NewEncoder(w).Encode(SearchResponse{
		Query:   q,
		Results: results,
		Total:   len(results),
	})
}

func (a *AppEngine) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	select {
	case <-a.Engine.IndexingDone:
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
	w.Header().Set("Content-Type", "application/json")

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "empty query"})
		return
	}

	results, err := a.Engine.Search(q)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "search error"})
		return
	}

	answer, err := a.Engine.GenerateAnswer(q, results)
	if err != nil {
		log.Error().Msgf("[ai] Ollama error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "AI service is unavailable"})
		return
	}

	json.NewEncoder(w).Encode(AIResponse{
		Query:  q,
		Answer: answer,
	})
}

func (a *AppEngine) SitemapHandler(w http.ResponseWriter, r *http.Request) {
	a.Engine.SitemapMu.RLock()
	data := a.Engine.SitemapCache
	a.Engine.SitemapMu.RUnlock()

	if len(data) == 0 {
		http.Error(w, "Not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "max-age=86400") // 1 day
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

func (a *AppEngine) ServeSPAHandler(w http.ResponseWriter, r *http.Request) {
	a.serveSPA(w, r, defaultTitle)
}

func (a *AppEngine) HelpHandler(w http.ResponseWriter, r *http.Request) {
	a.serveSPA(w, r, "Aide | Congopro")
}

func (a *AppEngine) PrivacyHandler(w http.ResponseWriter, r *http.Request) {
	a.serveSPA(w, r, "Confidentialité | Congopro")
}

func (a *AppEngine) TermsHandler(w http.ResponseWriter, r *http.Request) {
	a.serveSPA(w, r, "Conditions d'utilisation | Congopro")
}

func (a *AppEngine) CompanyHandler(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/company/")
	slug = strings.Trim(slug, "/")

	title := defaultTitle
	if slug != "" {
		// Wait for indexing or fall back to default
		select {
		case <-a.Engine.IndexingDone:
			if company, err := a.Engine.FindBySlug(slug); err != nil {
				title = company.Name + " | Congopro"
			}
		default:
			// still indexing, use default
		}
	}

	a.serveSPA(w, r, title)
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

	eAds := ads.EligibleAds(q, now)

	if eAds == nil {
		eAds = []ads.AdWire{}
	} else if len(eAds) > 1 {
		rand.Shuffle(len(eAds), func(i, j int) {
			eAds[i], eAds[j] = eAds[j], eAds[i]
		})
	}

	// 75% of the time show 1 AD, 25% of the time show the configured MaxPerPage
	var maxAdsPerPage = ads.AdsConfig.MaxPerPage
	if maxAdsPerPage > 1 {
		if rand.Intn(100) < 75 { // 75% (0 to 74)
			maxAdsPerPage = 1
		}
	}

	resp := ads.AdResponse{
		Active:      ads.AdsConfig.Active,
		RotationSec: ads.AdsConfig.RotationSec,
		MaxPerPage:  maxAdsPerPage,
		Ads:         eAds,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache") // needed
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

	data := struct {
		CSSVersion string
		Nonce      string
	}{
		CSSVersion: cssHash,
		Nonce:      nonce,
	}
	adsPreviewTmpl.Execute(w, data)
}

var (
	langSubscriptionsPathRegex = regexp.MustCompile(`^(/(fr|en))?/subscriptions/?$`)
	langCompanyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/company/([^/]+)/?$`)
	langHelpPathRegex          = regexp.MustCompile(`^(/(fr|en))?/(about|contact|faq|help)/?$`)
	langPrivacyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/privacy/?$`)
	langTermsPathRegex         = regexp.MustCompile(`^(/(fr|en))?/terms/?$`)
)

func (a *AppEngine) FrontendHandler(w http.ResponseWriter, r *http.Request) {
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
		http.NotFound(w, r)
		return
	}

	a.ServeSPAHandler(w, r)
}

func (a *AppEngine) serveSPA(w http.ResponseWriter, r *http.Request, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	nonce, _ := r.Context().Value(constants.NonceKey).(string)

	data := struct {
		CSSVersion   string
		Title        string
		Nonce        string
		CanonicalURL string
	}{
		CSSVersion:   cssHash,
		Title:        title,
		Nonce:        nonce,
		CanonicalURL: canonicalURL(r),
	}
	indexTmpl.Execute(w, data)
}

func FaviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=31536000") // 1 year
	http.ServeContent(w, r, "favicon.ico", startupTime, bytes.NewReader(web.FaviconICO))
}

func RobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeContent(w, r, "style.min.css", startupTime, bytes.NewReader(web.TailwindCSS))
}

func canonicalURL(r *http.Request) string {
	const host = "https://www.congopro.com"

	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	return host + path
}
