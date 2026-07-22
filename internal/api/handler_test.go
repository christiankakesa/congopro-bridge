package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"congopro-bridge/internal/config"
	"congopro-bridge/internal/data"
)

func TestGenerateNonce_ValidBase64OfExpectedLength(t *testing.T) {
	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		t.Fatalf("nonce is not valid base64: %v", err)
	}
	if len(decoded) != 16 {
		t.Fatalf("expected 16 decoded bytes, got %d", len(decoded))
	}
}

func TestGenerateNonce_IsUniquePerCall(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		nonce, err := generateNonce()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("nonce repeated across calls: %q", nonce)
		}
		seen[nonce] = struct{}{}
	}
}

func newTestAppEngine(allowedOrigin string) *AppEngine {
	cfg := &config.Config{AllowedOrigin: allowedOrigin, MeiliIndexName: "test"}
	return &AppEngine{Engine: data.NewEngine(cfg)}
}

func TestWithCORS_EmptyAllowedOriginSendsNoCORSHeaders(t *testing.T) {
	a := newTestAppEngine("")
	handler := a.WithCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Access-Control-Allow-Origin header, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("expected no Vary header, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected downstream handler to run, got status %d", rec.Code)
	}
}

func TestWithCORS_WildcardOriginSendsWildcardHeader(t *testing.T) {
	a := newTestAppEngine("*")
	handler := a.WithCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard Access-Control-Allow-Origin, got %q", got)
	}
}

func TestWithCORS_SpecificOriginSendsVaryHeader(t *testing.T) {
	a := newTestAppEngine("https://example.com")
	handler := a.WithCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("expected configured origin, got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary: Origin for a specific configured origin, got %q", got)
	}
}
