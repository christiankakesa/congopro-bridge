package api

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/ads"
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

var (
	startupTime = time.Now()
	cssHash     string
	indexTmpl   *template.Template
)

func init() {
	cssHash = fmt.Sprintf("%.8x", md5.Sum(web.TailwindCSS))
	indexTmpl = template.Must(template.New("index").Parse(string(web.IndexHTML)))
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

	results, err := a.Engine.HybridSearch(q)
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

	results, err := a.Engine.HybridSearch(q)
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

func RobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(web.RobotsTXT)))
	w.Write(web.RobotsTXT)
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

var (
	langSubscriptionsPathRegex = regexp.MustCompile(`^(/(fr|en))?/subscriptions/?$`)
	langCompanyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/company/([^/]+)/?$`)
	langHelpPathRegex          = regexp.MustCompile(`^(/(fr|en))?/(about|contact|faq|help)/?$`)
	langPrivacyPathRegex       = regexp.MustCompile(`^(/(fr|en))?/privacy/?$`)
	langTermsPathRegex         = regexp.MustCompile(`^(/(fr|en))?/terms/?$`)
)

func ServeSPAHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	data := struct {
		CSSVersion string
	}{
		CSSVersion: cssHash,
	}
	indexTmpl.Execute(w, data)
}

func FrontendHandler(w http.ResponseWriter, r *http.Request) {
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

	ServeSPAHandler(w, r)
}

func FaviconHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeContent(w, r, "favicon.ico", startupTime, bytes.NewReader(web.FaviconICO))
}

func FontsHandler(w http.ResponseWriter, r *http.Request) {
	fileName := strings.TrimPrefix(r.URL.Path, "/fonts/")
	content, err := web.FontsFS.ReadFile("fonts/" + fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	http.ServeContent(w, r, fileName, startupTime, strings.NewReader(string(content)))
}

func TailwindCssHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeContent(w, r, "style.min.css", startupTime, bytes.NewReader(web.TailwindCSS))
}

func ContentHandler(w http.ResponseWriter, r *http.Request) {
	page := strings.TrimPrefix(r.URL.Path, "/content/")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	var content []byte
	switch page {
	case "help":
		content = web.HelpHTML
	case "privacy":
		content = web.PrivacyHTML
	case "terms":
		content = web.TermsHTML
	default:
		http.NotFound(w, r)
		return
	}

	w.Write(content)
}

func AdsHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	now := time.Now()

	eAds := ads.EligibleAds(q, now)

	if eAds == nil {
		eAds = []ads.AdWire{}
	}

	resp := ads.AdResponse{
		Active:      ads.AdsConfig.Active,
		RotationSec: ads.AdsConfig.RotationSec,
		MaxPerPage:  ads.AdsConfig.MaxPerPage,
		Ads:         eAds,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=30")
	json.NewEncoder(w).Encode(resp)
}
