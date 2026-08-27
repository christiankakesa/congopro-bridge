package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Customers return from Stripe Checkout via a cross-site top-level GET.
// SameSite=Strict withholds the cookie on that navigation, so a customer who
// had just paid landed on the login page looking signed out. Lax still
// withholds it on cross-site POST, which is where the CSRF risk lives.
func TestCustomerSessionCookieIsLax(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://congopro.com/account", nil)
	setCustomerSessionCookie(w, r, "tok", 3600)

	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cs))
	}
	c := cs[0]
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax — Strict breaks the return from Stripe Checkout", c.SameSite)
	}
	if !c.HttpOnly {
		t.Error("session cookie must stay HttpOnly")
	}
	if c.Path != "/account" {
		t.Errorf("Path = %q, want /account", c.Path)
	}
}

// The admin cookie has no external redirect flow and stays Strict.
func TestAdminSessionCookieStaysStrict(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://congopro.com/admin", nil)
	setSessionCookie(w, r, "tok", 3600)

	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cs))
	}
	if cs[0].SameSite != http.SameSiteStrictMode {
		t.Errorf("admin SameSite = %v, want Strict", cs[0].SameSite)
	}
}
