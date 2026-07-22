package ratelimiter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP_UntrustedPeerIgnoresForwardingHeaders(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	req.Header.Set("X-Real-IP", "8.8.8.8")

	if got := realIP(req); got != "203.0.113.5" {
		t.Fatalf("expected direct peer IP for untrusted proxy, got %q", got)
	}
}

func TestRealIP_TrustedPeerHonorsForwardedFor(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 127.0.0.1")

	if got := realIP(req); got != "198.51.100.7" {
		t.Fatalf("expected forwarded client IP from trusted proxy, got %q", got)
	}
}

func TestRealIP_TrustedPeerHonorsRealIPHeader(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "198.51.100.9")

	if got := realIP(req); got != "198.51.100.9" {
		t.Fatalf("expected X-Real-IP from trusted proxy, got %q", got)
	}
}

func TestRealIP_NoTrustedProxiesConfigured(t *testing.T) {
	SetTrustedProxies(nil)
	t.Cleanup(func() { SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"}) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	if got := realIP(req); got != "127.0.0.1" {
		t.Fatalf("expected direct peer IP when no proxies are trusted, got %q", got)
	}
}

func TestSetTrustedProxies_NormalizesBareIPs(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.5", "not-an-ip", "192.168.1.0/24"})
	t.Cleanup(func() { SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"}) })

	if len(trustedProxies) != 2 {
		t.Fatalf("expected 2 valid entries (bare IP as /32 + CIDR), got %d", len(trustedProxies))
	}
}

func TestWithRateLimit_BlocksAfterLimitExceeded(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"})

	rl := NewRateLimiter(1) // 1 request per minute, burst of 1
	handler := rl.WithRateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:1111"

	rec1 := httptest.NewRecorder()
	handler(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second immediate request to be rate limited, got %d", rec2.Code)
	}
}

func TestWithRateLimit_SpoofedForwardedForDoesNotBypassLimitWhenUntrusted(t *testing.T) {
	SetTrustedProxies([]string{"127.0.0.1/32", "::1/128"})

	rl := NewRateLimiter(1)
	handler := rl.WithRateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	newReq := func(forwardedFor string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.1:1111" // same untrusted peer every time
		req.Header.Set("X-Forwarded-For", forwardedFor)
		return req
	}

	rec1 := httptest.NewRecorder()
	handler(rec1, newReq("1.1.1.1"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", rec1.Code)
	}

	// Attacker changes the spoofed header on every request; since the peer isn't
	// trusted, this must NOT grant a fresh rate-limit bucket.
	rec2 := httptest.NewRecorder()
	handler(rec2, newReq("2.2.2.2"))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed X-Forwarded-For to be ignored and request rate limited, got %d", rec2.Code)
	}
}
