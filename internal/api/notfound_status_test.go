package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"congopro-bridge/internal/constants"
)

// The branded 404 must carry the right status and headers, not just markup.
func TestRenderNotFound_StatusAndHeaders(t *testing.T) {
	a := &AppEngine{}
	r := httptest.NewRequest(http.MethodGet, "https://congopro.com/nope", nil)
	r = r.WithContext(context.WithValue(r.Context(), constants.NonceKey, "TESTNONCE"))
	w := httptest.NewRecorder()

	a.renderNotFound(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Cette page n'existe pas") {
		t.Error("body is not the branded 404 page")
	}
	if !strings.Contains(body, "TESTNONCE") {
		t.Error("CSP nonce not propagated into the 404 page")
	}
}
