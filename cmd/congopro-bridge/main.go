package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"congopro-bridge/internal/ads"
	"congopro-bridge/internal/api"
	"congopro-bridge/internal/data"
)

func main() {
	ads.LoadAds()

	engine := data.NewEngine()
	go func() {
		start := time.Now()
		if err := engine.LoadAndIndex(); err != nil {
			log.Fatalf("[startup] indexing failed: %v", err)
		}
		log.Printf("[startup] indexing completed in %s", time.Since(start).Round(time.Millisecond))
	}()

	apiAppEngine := &api.AppEngine{Engine: engine}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /search", api.WithCORS(apiAppEngine.SearchHandler))
	mux.HandleFunc("GET /ask", api.WithCORS(apiAppEngine.AIAnswerHandler))
	mux.HandleFunc("GET /ads", api.WithCORS(api.AdsHandler))
	mux.HandleFunc("GET /health", api.WithCORS(apiAppEngine.HealthHandler))
	mux.HandleFunc("GET /js/tailwind-cdn.js", api.TailwindCssHandler)
	mux.HandleFunc("GET /favicon.ico", api.FaviconHandler)

	mux.HandleFunc("GET /content/", api.WithCORS(api.ContentHandler))

	mux.HandleFunc("GET /company/", api.ServeSPAHandler)
	mux.HandleFunc("GET /help", api.ServeSPAHandler)
	mux.HandleFunc("GET /privacy", api.ServeSPAHandler)
	mux.HandleFunc("GET /terms", api.ServeSPAHandler)

	mux.HandleFunc("/", api.FrontendHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[server] listening on http://localhost%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[server] listen error: %v", err)
		}
	}()

	<-stop
	log.Println("[server] shutdown signal received, finishing active requests...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("[server] forced shutdown due to error/timeout: %v", err)
	}

	log.Println("[server] successfully stopped")
}
