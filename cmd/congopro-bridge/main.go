package main

import (
	"context"
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
	"congopro-bridge/internal/logger"
	"congopro-bridge/internal/middlewares/ratelimiter"
)

func main() {
	logLevel := logger.DetectLogLevel()
	logType := logger.DetectLogType()
	if logType == logger.Terminal {
		logType = logger.Application
	}
	logger.Init(logType, logger.Options{Level: logLevel})

	cfg := config.Load()

	ads.LoadAds()

	engine := data.NewEngine(cfg)

	go func() {
		start := time.Now()
		if err := engine.LoadAndIndex(); err != nil {
			log.Fatal().Msgf("[startup] indexing failed: %v", err)
		}
		log.Info().Msgf("[startup] indexing completed in %s", time.Since(start).Round(time.Millisecond))
	}()

	apiAppEngine := &api.AppEngine{Engine: engine}

	mux := http.NewServeMux()

	// Static
	mux.HandleFunc("GET /favicon.ico", api.FaviconHandler)
	mux.HandleFunc("GET /robots.txt", api.RobotsTxt)
	mux.HandleFunc("GET /site.webmanifest", api.ServeManifest)
	mux.HandleFunc("GET /fonts/", api.FontsHandler)
	mux.HandleFunc("GET /images/", api.ImagesHandler)
	mux.HandleFunc("GET /css/style.min.css", api.TailwindCssHandler)

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
	mux.HandleFunc("GET /api/v1/health", apiAppEngine.WithCORS(apiAppEngine.HealthHandler))

	// Serves old company routes
	mux.HandleFunc("GET /company/", apiAppEngine.WithSecurityHeaders(apiAppEngine.CompanyHandler))

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
