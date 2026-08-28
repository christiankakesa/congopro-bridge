//go:build integration

// End-to-end account flow through the REAL handlers, mux wiring, and local
// Postgres, with the OTP email captured instead of sent. Run via
// `make dev-test-integration`.
package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/mail"
)

type captureMailer struct {
	mu       sync.Mutex
	messages []mail.Message
}

func (c *captureMailer) Send(msg mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg)
	return nil
}

func (c *captureMailer) lastCode(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		t.Fatal("no OTP email captured")
	}
	m := c.re()
	return m.FindString(c.messages[len(c.messages)-1].Body)
}

func (c *captureMailer) re() *regexp.Regexp { return regexp.MustCompile(`\b[0-9]{6}\b`) }

// newCapturerAndEmail: just the mail capturer and a unique identity, for
// tests that build their own mux.
func newCapturerAndEmail(t *testing.T) (*captureMailer, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set — run via make dev-test-integration")
	}
	email := "flow" + strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(t.Name(), "Test"), "_", "")) + "@test.congopro.local"
	return &captureMailer{}, email
}

func newAccountTestServer(t *testing.T) (*httptest.Server, *captureMailer, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set — run via make dev-test-integration")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	capturer := &captureMailer{}
	a := &AppEngine{DB: pool, Mailer: capturer, MailEnabled: true}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /account", a.WithSecurityHeaders(a.RequireCustomerAuth(a.AccountDashboardHandler)))
	mux.HandleFunc("GET /account/login", a.WithSecurityHeaders(a.AccountLoginPageHandler))
	mux.HandleFunc("POST /account/login", a.WithSecurityHeaders(a.AccountRequestCodeHandler))
	mux.HandleFunc("GET /account/verify", a.WithSecurityHeaders(a.AccountVerifyPageHandler))
	mux.HandleFunc("POST /account/verify", a.WithSecurityHeaders(a.AccountVerifyCodeHandler))
	mux.HandleFunc("POST /account/logout", a.WithSecurityHeaders(a.AccountLogoutHandler))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, capturer, "flow" + strings.ReplaceAll(strings.ToLower(t.Name()), "_", "") + "@test.congopro.local"
}

func postNoRedirect(t *testing.T, c *http.Client, action string, form url.Values) *http.Response {
	t.Helper()
	resp, err := c.PostForm(action, form)
	if err != nil {
		t.Fatalf("POST %s: %v", action, err)
	}
	resp.Body.Close()
	return resp
}

func TestAccountOTPFlow(t *testing.T) {
	srv, capturer, email := newAccountTestServer(t)

	// Cleanup the identity this test creates.
	pool, _ := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM customers WHERE email = $1`, email)
		pool.Exec(context.Background(), `DELETE FROM otp_codes WHERE email = $1`, email)
		pool.Close()
	})

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		// Inspect 303s directly instead of silently following them.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Dashboard redirects anonymous visitors to the login page (no-follow
	// client: assert the Location header).
	resp, err := client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/account/login" {
		t.Fatalf("anonymous /account redirected to %q, want /account/login", loc)
	}

	// Request a code.
	r := postNoRedirect(t, client, srv.URL+"/account/login", url.Values{"email": {email}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("code request status = %d, want 303", r.StatusCode)
	}
	verifyURL := r.Header.Get("Location")
	if !strings.Contains(verifyURL, "/account/verify?e=") {
		t.Fatalf("code request redirected to %q", verifyURL)
	}

	// Wrong code is rejected generically.
	r = postNoRedirect(t, client, srv.URL+"/account/verify", url.Values{"email": {email}, "code": {"000000"}})
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d, want 401", r.StatusCode)
	}

	// Right code from the captured email logs in and lands on the dashboard.
	code := capturer.lastCode(t)
	r = postNoRedirect(t, client, srv.URL+"/account/verify", url.Values{"email": {email}, "code": {code}})
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/account" {
		t.Fatalf("verify status = %d Location = %q, want 303 /account", r.StatusCode, r.Header.Get("Location"))
	}

	resp, err = client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 1<<16)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), email) {
		t.Fatalf("dashboard does not show the signed-in email:\n%s", string(body[:n]))
	}

	// Logout clears the session server-side: /account redirects again.
	postNoRedirect(t, client, srv.URL+"/account/logout", nil)
	resp, err = client.Get(srv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/account/login" {
		t.Fatalf("after logout /account redirected to %q, want /account/login", loc)
	}
}

func TestAccountRequestCodeValidation(t *testing.T) {
	srv, _, _ := newAccountTestServer(t)
	client := &http.Client{}

	r := postNoRedirect(t, client, srv.URL+"/account/login", url.Values{"email": {"not-an-email"}})
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid email status = %d, want 422", r.StatusCode)
	}
}

// Without a mailer, requesting a code degrades to a clean 503 — the handler
// checks MailEnabled before touching anything else, so no DB is needed here.
func TestAccountEmailDisabled(t *testing.T) {
	a := &AppEngine{Mailer: nil, MailEnabled: false}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/login", a.WithSecurityHeaders(a.AccountRequestCodeHandler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r := postNoRedirect(t, srv.Client(), srv.URL+"/account/login", url.Values{"email": {"someone@example.cd"}})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("code request without email config: status = %d, want 503", r.StatusCode)
	}
}

// TestAccountClaimFlow: full HTTP round-trip — customer logs in via OTP,
// claims a company, a staff session approves it, the company ends up
// claimed, and the decision email is captured.
func TestAccountClaimFlow(t *testing.T) {
	capturer, email := newCapturerAndEmail(t)
	pool, _ := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))

	// Fixtures: a staff user with a session cookie, a claimable company.
	staffHash, _ := auth.HashPassword("staff-password-long-enough")
	var staffID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, name, password_hash, role) VALUES ($1, 'Flow Staff', $2, 'support') RETURNING id`,
		"flow-staff-"+strings.ToLower(strings.ReplaceAll(t.Name(), "_", ""))+"@test.congopro.local", staffHash).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	staffToken, _, err := auth.CreateSession(context.Background(), pool, staffID, "test", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	companyID := "flow-co-" + strings.ToLower(strings.ReplaceAll(t.Name(), "_", ""))
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO companies (id, name, name_seo, status) VALUES ($1, 'Flow SARL', $1, 'published')`, companyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM company_claims WHERE company_id = $1`, companyID)
		pool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, companyID)
		pool.Exec(context.Background(), `DELETE FROM sessions WHERE token = $1`, staffToken)
		pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, staffID)
		pool.Exec(context.Background(), `DELETE FROM customers WHERE email = $1`, email)
		pool.Exec(context.Background(), `DELETE FROM otp_codes WHERE email = $1`, email)
		pool.Close()
	})

	// The full account mux (customer side)…
	tg := newCaptureNotifier()
	a := &AppEngine{DB: pool, Mailer: capturer, MailEnabled: true, Telegram: tg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /account", a.WithSecurityHeaders(a.RequireCustomerAuth(a.AccountDashboardHandler)))
	mux.HandleFunc("GET /account/login", a.WithSecurityHeaders(a.AccountLoginPageHandler))
	mux.HandleFunc("POST /account/login", a.WithSecurityHeaders(a.AccountRequestCodeHandler))
	mux.HandleFunc("GET /account/verify", a.WithSecurityHeaders(a.AccountVerifyPageHandler))
	mux.HandleFunc("POST /account/verify", a.WithSecurityHeaders(a.AccountVerifyCodeHandler))
	mux.HandleFunc("GET /account/claim", a.WithSecurityHeaders(a.RequireCustomerAuth(a.AccountClaimFormHandler)))
	mux.HandleFunc("POST /account/claim", a.WithSecurityHeaders(a.RequireCustomerAuth(a.AccountClaimSubmitHandler)))
	mux.HandleFunc("GET /admin/claims", a.WithSecurityHeaders(a.RequireStaffAuth(a.AdminClaimsListHandler)))
	mux.HandleFunc("POST /admin/claims/{id}/approve", a.WithSecurityHeaders(a.RequireStaffAuth(a.AdminClaimApproveHandler)))
	claimSrv := httptest.NewServer(mux)
	t.Cleanup(claimSrv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Login as the customer.
	r := postNoRedirect(t, client, claimSrv.URL+"/account/login", url.Values{"email": {email}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("code request: %d", r.StatusCode)
	}
	code := capturer.lastCode(t)
	r = postNoRedirect(t, client, claimSrv.URL+"/account/verify", url.Values{"email": {email}, "code": {code}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("verify: %d", r.StatusCode)
	}

	// Claim the company.
	r = postNoRedirect(t, client, claimSrv.URL+"/account/claim", url.Values{
		"company_slug": {companyID},
		"relationship": {"owner"},
		"phone":        {"+243900000002"},
		"evidence":     {"Je suis le fondateur, RCCM CD/KNG/15-A-12345 à l'appui."},
	})
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/account" {
		t.Fatalf("claim submit: %d %q", r.StatusCode, r.Header.Get("Location"))
	}

	// The staff chat hears about it, with the company, the claimant, and a
	// deep link into the admin queue.
	if msg := tg.waitOne(t); !strings.Contains(msg, "Nouvelle réclamation") ||
		!strings.Contains(msg, "Flow SARL") || !strings.Contains(msg, "/admin/claims") {
		t.Fatalf("claim notification = %q", msg)
	}

	// The claim appears in the customer's dashboard.
	resp, err := client.Get(claimSrv.URL + "/account")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	resp.Body.Close()
	if !strings.Contains(body, "Flow SARL") {
		t.Fatal("dashboard does not list the claim")
	}

	// Staff sees it in the queue and approves it.
	staffClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	staffReq, _ := http.NewRequest(http.MethodGet, claimSrv.URL+"/admin/claims?status=pending", nil)
	staffReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: staffToken, Path: "/admin"})
	sresp, err := staffClient.Do(staffReq)
	if err != nil {
		t.Fatal(err)
	}
	qbody := readAll(t, sresp)
	sresp.Body.Close()
	if !strings.Contains(qbody, email) {
		t.Fatal("claim not visible in the admin queue")
	}

	var claimPath string
	if m := regexp.MustCompile(`/admin/claims/([0-9a-f-]{36})/approve`).FindString(qbody); m != "" {
		claimPath = m
	} else {
		t.Fatal("no approve formaction found in queue")
	}
	approveReq, _ := http.NewRequest(http.MethodPost, claimSrv.URL+claimPath, strings.NewReader("note=documents+conformes"))
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: staffToken, Path: "/admin"})
	aresp, err := staffClient.Do(approveReq)
	if err != nil {
		t.Fatal(err)
	}
	aresp.Body.Close()
	if aresp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve: %d", aresp.StatusCode)
	}

	// Durable effect + decision email.
	var claimer string
	if err := pool.QueryRow(context.Background(),
		`SELECT claimed_by_customer_id::text FROM companies WHERE id = $1`, companyID).Scan(&claimer); err != nil || claimer == "" {
		t.Fatalf("company not claimed after approve: %v %q", err, claimer)
	}
	capturer.mu.Lock()
	last := capturer.messages[len(capturer.messages)-1]
	capturer.mu.Unlock()
	if !strings.Contains(last.Subject, "réclamation") || !strings.Contains(last.Body, "approuvée") {
		t.Fatalf("decision email wrong: %q / %q", last.Subject, last.Body)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := make([]byte, 1<<18)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}
