package main

import (
	"context"
	"flag"
	golog "log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/ads"
	"congopro-bridge/internal/api"
	"congopro-bridge/internal/config"
	"congopro-bridge/internal/data"
	"congopro-bridge/internal/db"
	"congopro-bridge/internal/logger"
	"congopro-bridge/internal/mail"
	"congopro-bridge/internal/middlewares/ratelimiter"
)

func main() {
	migrateFlag := flag.Bool("migrate", false, "apply pending database migrations and exit")
	importFlag := flag.Bool("import", false, "import companies from the embedded JSON into postgres and exit")
	createAdminFlag := flag.Bool("create-admin", false, "interactively create the first staff account and exit")
	flag.Parse()

	logLevel := logger.DetectLogLevel()
	logType := logger.DetectLogType()
	if logType == logger.Terminal {
		logType = logger.Application
	}
	logger.Init(logType, logger.Options{Level: logLevel})

	cfg := config.Load()
	ctx := context.Background()

	if *migrateFlag {
		if cfg.DatabaseURL == "" {
			log.Fatal().Msg("[migrate] DATABASE_URL is not set")
		}
		if err := db.Migrate(cfg.DatabaseURL); err != nil {
			log.Fatal().Msgf("[migrate] failed: %v", err)
		}
		log.Info().Msg("[migrate] database is up to date")
		return
	}

	if *importFlag {
		if cfg.DatabaseURL == "" {
			log.Fatal().Msg("[import] DATABASE_URL is not set")
		}
		pool, err := db.New(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatal().Msgf("[import] failed to connect: %v", err)
		}
		defer pool.Close()
		n, err := data.ImportFromEmbeddedJSON(ctx, pool)
		if err != nil {
			log.Fatal().Msgf("[import] failed: %v", err)
		}
		log.Info().Msgf("[import] imported %d companies", n)
		return
	}

	if *createAdminFlag {
		if cfg.DatabaseURL == "" {
			log.Fatal().Msg("[create-admin] DATABASE_URL is not set")
		}
		pool, err := db.New(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Fatal().Msgf("[create-admin] failed to connect: %v", err)
		}
		defer pool.Close()
		if err := createAdmin(ctx, pool); err != nil {
			log.Fatal().Msgf("[create-admin] failed: %v", err)
		}
		return
	}

	if cfg.MeiliMasterKey == "" {
		log.Warn().Msg("[startup] MEILI_MASTER_KEY is empty — Meilisearch is running without authentication, anyone reachable on MEILI_URL has full read/write access. Set MEILI_MASTER_KEY except for local, network-isolated development.")
	}
	if cfg.AllowedOrigin == "*" {
		log.Warn().Msg("[startup] ALLOWED_ORIGIN is \"*\" — any website can call the API cross-origin. Set ALLOWED_ORIGIN to a specific origin unless third-party cross-origin access is intentional.")
	}
	if cfg.DatabaseURL == "" {
		log.Fatal().Msg("[startup] DATABASE_URL is not set")
	}
	// All-or-nothing SMTP contract: a half-configured account only fails
	// later, when a customer is waiting for a code. Empty SMTP_HOST (email
	// disabled) is valid.
	if err := cfg.ValidateSMTP(); err != nil {
		log.Fatal().Msgf("[startup] invalid SMTP configuration: %v", err)
	}

	ratelimiter.SetTrustedProxies(cfg.TrustedProxies)

	ads.LoadAds()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Msgf("[startup] failed to connect to database: %v", err)
	}
	defer pool.Close()

	engine := data.NewEngine(cfg, pool)

	go func() {
		start := time.Now()
		if err := engine.LoadAndIndex(); err != nil {
			log.Error().Msgf("[startup] indexing failed after retries — search and AI endpoints will return errors, but static pages and company profiles still work: %v", err)
			return
		}
		log.Info().Msgf("[startup] indexing completed in %s", time.Since(start).Round(time.Millisecond))
	}()

	apiAppEngine := &api.AppEngine{Engine: engine, DB: pool}

	// Transactional email (customer OTP). Empty SMTP_HOST disables email —
	// account login then answers 503 instead of half-working.
	if mailCfg, enabled := cfg.MailConfig(); enabled {
		apiAppEngine.Mailer = mail.SMTPSender{Config: mailCfg}
		apiAppEngine.MailEnabled = true
		log.Info().Msgf("[startup] email enabled via %s:%d (SMTP_TLS=%s)", mailCfg.Host, mailCfg.Port, mailCfg.TLSMode)
	} else {
		log.Info().Msg("[startup] email disabled (SMTP_HOST empty) — /account login will return 503")
	}

	mux := http.NewServeMux()

	// Static
	mux.HandleFunc("GET /favicon.ico", api.FaviconHandler)
	mux.HandleFunc("GET /robots.txt", api.RobotsTxt)
	mux.HandleFunc("GET /site.webmanifest", api.ServeManifest)
	mux.HandleFunc("GET /fonts/", api.FontsHandler)
	mux.HandleFunc("GET /images/", api.ImagesHandler)
	mux.HandleFunc("GET /css/style.min.css", api.TailwindCssHandler)
	mux.HandleFunc("GET /js/htmx.min.js", api.HtmxJSHandler)

	// Static pages
	mux.HandleFunc("GET /help", apiAppEngine.WithSecurityHeaders(apiAppEngine.HelpHandler))
	mux.HandleFunc("GET /privacy", apiAppEngine.WithSecurityHeaders(apiAppEngine.PrivacyHandler))
	mux.HandleFunc("GET /terms", apiAppEngine.WithSecurityHeaders(apiAppEngine.TermsHandler))
	mux.HandleFunc("GET /sitemap.xml.gz", apiAppEngine.SitemapHandler)

	// Ads preview
	mux.HandleFunc("GET /ads-preview", apiAppEngine.WithSecurityHeaders(apiAppEngine.AdsPreviewPageHandler))

	// Search API
	searchRL := ratelimiter.NewRateLimiter(60)
	askRL := ratelimiter.NewRateLimiter(10)
	adsRL := ratelimiter.NewRateLimiter(30)
	contentRL := ratelimiter.NewRateLimiter(20)
	adsPreviewRL := ratelimiter.NewRateLimiter(10)
	mux.HandleFunc("GET /api/v1/search", apiAppEngine.WithCORS(searchRL.WithRateLimit(apiAppEngine.SearchHandler)))
	mux.HandleFunc("GET /api/v1/ask", apiAppEngine.WithCORS(askRL.WithRateLimit(apiAppEngine.AIAnswerHandler)))
	mux.HandleFunc("GET /api/v1/ads", apiAppEngine.WithCORS(adsRL.WithRateLimit(apiAppEngine.AdsHandler)))
	mux.HandleFunc("GET /api/v1/content/", apiAppEngine.WithCORS(contentRL.WithRateLimit(apiAppEngine.ContentHandler)))
	mux.HandleFunc("GET /api/v1/ads-preview-data", apiAppEngine.WithCORS(adsPreviewRL.WithRateLimit(apiAppEngine.AdsPreviewDataHandler)))
	mux.HandleFunc("GET /api/v1/healthz", apiAppEngine.WithCORS(apiAppEngine.HealthzHandler))

	// Serves old company routes
	mux.HandleFunc("GET /company/", apiAppEngine.WithSecurityHeaders(apiAppEngine.CompanyHandler))

	// Admin (staff auth required beyond login itself)
	adminLoginRL := ratelimiter.NewRateLimiter(10)
	mux.HandleFunc("GET /admin/login", apiAppEngine.WithSecurityHeaders(apiAppEngine.AdminLoginFormHandler))
	mux.HandleFunc("POST /admin/login", apiAppEngine.WithSecurityHeaders(adminLoginRL.WithRateLimit(apiAppEngine.AdminLoginHandler)))
	mux.HandleFunc("POST /admin/logout", apiAppEngine.WithSecurityHeaders(apiAppEngine.AdminLogoutHandler))
	mux.HandleFunc("GET /admin", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminCompaniesListHandler)))
	mux.HandleFunc("GET /admin/companies/new", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminCompanyNewFormHandler)))
	mux.HandleFunc("POST /admin/companies/new", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminCompanyCreateHandler)))
	mux.HandleFunc("GET /admin/companies/{id}/edit", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminCompanyEditFormHandler)))
	mux.HandleFunc("POST /admin/companies/{id}/edit", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminCompanyUpdateHandler)))
	mux.HandleFunc("GET /admin/claims", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminClaimsListHandler)))
	mux.HandleFunc("POST /admin/claims/{id}/approve", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminClaimApproveHandler)))
	mux.HandleFunc("POST /admin/claims/{id}/reject", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireStaffAuth(apiAppEngine.AdminClaimRejectHandler)))

	// Customer accounts (passwordless email OTP)
	otpRequestRL := ratelimiter.NewRateLimiter(5) // sending mail costs money
	otpVerifyRL := ratelimiter.NewRateLimiter(10) // per-code attempt cap is the real guard
	mux.HandleFunc("GET /account", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireCustomerAuth(apiAppEngine.AccountDashboardHandler)))
	mux.HandleFunc("GET /account/login", apiAppEngine.WithSecurityHeaders(apiAppEngine.AccountLoginPageHandler))
	mux.HandleFunc("POST /account/login", apiAppEngine.WithSecurityHeaders(otpRequestRL.WithRateLimit(apiAppEngine.AccountRequestCodeHandler)))
	mux.HandleFunc("GET /account/verify", apiAppEngine.WithSecurityHeaders(apiAppEngine.AccountVerifyPageHandler))
	mux.HandleFunc("POST /account/verify", apiAppEngine.WithSecurityHeaders(otpVerifyRL.WithRateLimit(apiAppEngine.AccountVerifyCodeHandler)))
	mux.HandleFunc("POST /account/logout", apiAppEngine.WithSecurityHeaders(apiAppEngine.AccountLogoutHandler))

	// Customer claims on companies (behind customer auth)
	claimSubmitRL := ratelimiter.NewRateLimiter(5)
	mux.HandleFunc("GET /account/claim", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireCustomerAuth(apiAppEngine.AccountClaimFormHandler)))
	mux.HandleFunc("POST /account/claim", apiAppEngine.WithSecurityHeaders(apiAppEngine.RequireCustomerAuth(claimSubmitRL.WithRateLimit(apiAppEngine.AccountClaimSubmitHandler))))

	// Default routes
	mux.HandleFunc("/", apiAppEngine.WithSecurityHeaders(apiAppEngine.FrontendHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	handler := logger.AccessLogMiddleware(mux)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          golog.New(log.Logger.With().Str("component", "net/http").Logger(), "", 0),
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info().Msgf("[server] listening on http://localhost%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Msgf("[server] listen error: %v", err)
		}
	}()

	<-stop
	log.Info().Msg("[server] shutdown signal received, finishing active requests...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Msgf("[server] forced shutdown due to error/timeout: %v", err)
	}

	log.Info().Msg("[server] successfully stopped")
}
