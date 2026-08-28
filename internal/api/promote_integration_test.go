//go:build integration

// Promote flow + Stripe webhook handler tests — run via `make
// test-integration`. Webhook payloads are HMAC-signed locally (Stripe's
// documented format), the CheckoutCreator is a fake; no Stripe account
// needed.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/promotions"
)

// fakeCheckout returns deterministic session ids/urls.
type fakeCheckout struct {
	calls int
}

const fakeSessionID = "cs_test_fixed"

func (f *fakeCheckout) CreatePromotionSession(_ context.Context, promotionID, _, _, _ string) (string, string, error) {
	f.calls++
	return fakeSessionID, "https://checkout.example/" + promotionID, nil
}

func (f *fakeCheckout) PriceDisplay(context.Context) (PriceDisplay, bool) {
	return PriceDisplay{Amount: 1000, Currency: "usd", Interval: "month"}, true
}

// signPayload builds a Stripe-Signature header: t=…,v1=HMAC-SHA256(secret, "t.payload").
func signPayload(secret string, payload []byte, ts time.Time) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t + "."))
	mac.Write(payload)
	return fmt.Sprintf("t=%s,v1=%s", t, hex.EncodeToString(mac.Sum(nil)))
}

// cookieTransport injects the customer session cookie on every request —
// simpler than a jar for these flows, and keeps CheckRedirect controllable.
type cookieTransport struct{ token string }

func (c cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Cookie", customers.CustomerSessionCookieName+"="+c.token)
	return http.DefaultTransport.RoundTrip(req2)
}

func noRedirectClient(token string) *http.Client {
	return &http.Client{
		Transport:     cookieTransport{token: token},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type promoteFixture struct {
	srv        *httptest.Server
	pool       *pgxpool.Pool
	client     *http.Client
	email      string
	companyID  string
	checkout   *fakeCheckout
	webhookURL string
	telegram   *captureNotifier
}

// newPromoteFixture: a server with the promote+webhook routes, a customer
// with a session, and a company the customer owns (approved claim column).
func newPromoteFixture(t *testing.T, checkout *fakeCheckout, webhookSecret string) *promoteFixture {
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
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	tg := newCaptureNotifier()
	a := &AppEngine{
		DB: pool, Mailer: &captureMailer{}, MailEnabled: true,
		StripeCheckout: checkout,
		StripeEnabled:  checkout != nil,
		Telegram:       tg,
	}
	if webhookSecret != "" {
		a.StripeWebhookSecret = webhookSecret
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/promote", a.WithSecurityHeaders(a.RequireCustomerAuth(a.AccountPromoteCheckoutHandler)))
	mux.HandleFunc("POST /webhooks/stripe", a.StripeWebhookHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx := context.Background()
	email := "promoter-" + strings.ToLower(strings.TrimPrefix(t.Name(), "Test")) + "@test.congopro.local"
	cust, err := customers.CreateOrGetByEmail(ctx, pool, email)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := customers.CreateSession(ctx, pool, cust.ID, "test", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	companyID := "wh-co-" + time.Now().Format("150405000000000")
	if _, err := pool.Exec(ctx,
		`INSERT INTO companies (id, name, name_seo, status, claimed_by_customer_id)
		 VALUES ($1, 'WH SARL', $1, 'published', $2)`, companyID, cust.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM promotions WHERE company_id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM customer_sessions WHERE token = $1`, token)
		pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust.ID)
	})

	return &promoteFixture{
		srv: srv, pool: pool, client: noRedirectClient(token),
		email: email, companyID: companyID, checkout: checkout,
		webhookURL: srv.URL + "/webhooks/stripe",
		telegram:   tg,
	}
}

func postWebhook(t *testing.T, url, secret string, event map[string]any, signWith string) int {
	t.Helper()
	payload, _ := json.Marshal(event)
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", signPayload(signWith, payload, time.Now()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPromoteCheckoutGating(t *testing.T) {
	f := newPromoteFixture(t, &fakeCheckout{}, "")

	// Not the owner (fixture owns it — test the unknown company path).
	r := postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {"unknown-co"}})
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown company: %d, want 404", r.StatusCode)
	}

	// Owner: checkout opens, 303 to the fake session URL.
	r = postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {f.companyID}})
	if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(r.Header.Get("Location"), "https://checkout.example/") {
		t.Fatalf("owner checkout: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if f.checkout.calls != 1 {
		t.Fatalf("checkout calls = %d", f.checkout.calls)
	}

	// Second live promo on the same company: friendly 422.
	r = postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {f.companyID}})
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate live: %d, want 422", r.StatusCode)
	}
}

func TestPromoteStripeDisabled(t *testing.T) {
	f := newPromoteFixture(t, nil, "")
	r := postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {f.companyID}})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stripe disabled: %d, want 503", r.StatusCode)
	}
}

func TestPromoteOwnershipGate(t *testing.T) {
	f := newPromoteFixture(t, &fakeCheckout{}, "")
	// Strip ownership: promote must refuse with the claim-first message.
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE companies SET claimed_by_customer_id = NULL WHERE id = $1`, f.companyID); err != nil {
		t.Fatal(err)
	}
	r := postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {f.companyID}})
	if r.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("non-owner: %d, want 422", r.StatusCode)
	}
}

func TestStripeWebhookSignatureAndLifecycle(t *testing.T) {
	const secret = "whsec_test_secret"
	f := newPromoteFixture(t, &fakeCheckout{}, secret)
	ctx := context.Background()

	// Open a checkout so a pending row with a session id exists.
	r := postNoRedirect(t, f.client, f.srv.URL+"/account/promote", url.Values{"company_slug": {f.companyID}})
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("checkout open: %d", r.StatusCode)
	}

	completed := map[string]any{
		"id": "evt_test_1", "type": "checkout.session.completed",
		"data": map[string]any{"object": map[string]any{
			"id": fakeSessionID, "customer": "cus_test_wh", "subscription": "sub_test_wh",
		}},
	}

	// Bad signature → 400, state untouched.
	if code := postWebhook(t, f.webhookURL, secret, completed, "wrong-secret"); code != http.StatusBadRequest {
		t.Fatalf("bad signature: %d, want 400", code)
	}
	if ok, _ := promotions.IsPromoted(ctx, f.pool, f.companyID); ok {
		t.Fatal("state changed despite bad signature")
	}

	// Valid signature → 200, promotion active. The subscription retrieve
	// fails offline (best-effort by design) — activation must not depend
	// on it.
	if code := postWebhook(t, f.webhookURL, secret, completed, secret); code != http.StatusOK {
		t.Fatalf("valid signature: %d, want 200", code)
	}
	if ok, _ := promotions.IsPromoted(ctx, f.pool, f.companyID); !ok {
		t.Fatal("promotion not active after webhook")
	}
	if msg := f.telegram.waitOne(t); !strings.Contains(msg, "Mise en avant activée") || !strings.Contains(msg, "WH SARL") {
		t.Fatalf("activation notification = %q", msg)
	}

	// Stripe replays webhooks: the same completed event again touches zero
	// rows and must NOT re-notify the staff chat.
	if code := postWebhook(t, f.webhookURL, secret, completed, secret); code != http.StatusOK {
		t.Fatalf("replayed completed: %d, want 200", code)
	}
	f.telegram.expectNone(t)

	// subscription.updated with a period end fills what the failed
	// retrieve left empty.
	periodEnd := time.Now().AddDate(0, 1, 0).Unix()
	updated := map[string]any{
		"id": "evt_test_2", "type": "customer.subscription.updated",
		"data": map[string]any{"object": map[string]any{
			"id": "sub_test_wh", "status": "active",
			"items": map[string]any{"data": []any{map[string]any{"current_period_end": periodEnd}}},
		}},
	}
	if code := postWebhook(t, f.webhookURL, secret, updated, secret); code != http.StatusOK {
		t.Fatalf("subscription.updated: %d", code)
	}
	var status string
	var stored *time.Time
	if err := f.pool.QueryRow(ctx,
		`SELECT status, current_period_end FROM promotions WHERE stripe_subscription_id = 'sub_test_wh'`,
	).Scan(&status, &stored); err != nil || status != "active" || stored == nil {
		t.Fatalf("after update: status=%q period=%v err=%v", status, stored, err)
	}

	// subscription.deleted → canceled, promotion stops.
	deleted := map[string]any{
		"id": "evt_test_3", "type": "customer.subscription.deleted",
		"data": map[string]any{"object": map[string]any{"id": "sub_test_wh"}},
	}
	if code := postWebhook(t, f.webhookURL, secret, deleted, secret); code != http.StatusOK {
		t.Fatalf("subscription.deleted: %d", code)
	}
	if ok, _ := promotions.IsPromoted(ctx, f.pool, f.companyID); ok {
		t.Fatal("promotion must stop after subscription.deleted")
	}
	if msg := f.telegram.waitOne(t); !strings.Contains(msg, "Mise en avant résiliée") {
		t.Fatalf("cancellation notification = %q", msg)
	}

	// Replayed delete: zero rows → silence.
	if code := postWebhook(t, f.webhookURL, secret, deleted, secret); code != http.StatusOK {
		t.Fatalf("replayed deleted: %d, want 200", code)
	}
	f.telegram.expectNone(t)
}

func TestFormatPrice(t *testing.T) {
	if got := FormatPrice(PriceDisplay{Amount: 1000, Currency: "usd", Interval: "month"}); got != "10.00 $ / mois" {
		t.Fatalf("FormatPrice = %q", got)
	}
	if got := FormatPrice(PriceDisplay{Amount: 1550, Currency: "eur", Interval: "year"}); got != "15.50 € / an" {
		t.Fatalf("FormatPrice = %q", got)
	}
}
