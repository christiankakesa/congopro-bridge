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
)

func main() {
	logger.Init(false)
	cfg := config.Load()

	ads.LoadAds()

	engine := data.NewEngine(cfg)
	defer func() {
		if err := engine.Close(); err != nil {
			log.Error().Err(err).Msg("[shutdown] failed to close bleve index")
		}
	}()

	go func() {
		start := time.Now()
		if err := engine.LoadAndIndex(); err != nil {
			log.Fatal().Msgf("[startup] indexing failed: %v", err)
		}
		log.Info().Msgf("[startup] indexing completed in %s", time.Since(start).Round(time.Millisecond))
	}()

	apiAppEngine := &api.AppEngine{Engine: engine}

	mux := http.NewServeMux()

	// SEO
	mux.HandleFunc("GET /robots.txt", api.RobotsTxt)
	mux.HandleFunc("GET /sitemap.xml.gz", apiAppEngine.SitemapHandler)

	// Static
	mux.HandleFunc("GET /fonts/", api.FontsHandler)
	mux.HandleFunc("GET /css/style.min.css", api.TailwindCssHandler)
	mux.HandleFunc("GET /favicon.ico", api.FaviconHandler)

	// Static pages
	mux.HandleFunc("GET /content/", apiAppEngine.WithCORS(api.ContentHandler))
	mux.HandleFunc("GET /help", api.ServeSPAHandler)
	mux.HandleFunc("GET /privacy", api.ServeSPAHandler)
	mux.HandleFunc("GET /terms", api.ServeSPAHandler)

	// Search API
	mux.HandleFunc("GET /search", apiAppEngine.WithCORS(apiAppEngine.SearchHandler))
	mux.HandleFunc("GET /ask", apiAppEngine.WithCORS(apiAppEngine.AIAnswerHandler))
	mux.HandleFunc("GET /ads", apiAppEngine.WithCORS(api.AdsHandler))
	mux.HandleFunc("GET /health", apiAppEngine.WithCORS(apiAppEngine.HealthHandler))
	// Serves old company routes
	mux.HandleFunc("GET /company/", api.ServeSPAHandler)

	// Default routes
	mux.HandleFunc("/", api.FrontendHandler)

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
