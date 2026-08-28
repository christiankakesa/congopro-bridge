//go:build integration

// Run with: make dev-test-integration

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/customers"
)

// fakeSubReader serves canned amounts, or an error to exercise the
// degraded render. (Unique name — the api test package is shared across
// tagged and untagged files.)
type fakeSubReader struct {
	amounts map[string]SubAmount
	err     error
}

func (f *fakeSubReader) SubscriptionAmounts(context.Context, []string) (map[string]SubAmount, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.amounts, nil
}

type revenueFixture struct {
	srv   *httptest.Server
	email string
}

// newRevenueFixture: one company, one customer, one ACTIVE promotion with a
// fixed fake subscription id, and /admin/revenue registered with the staff
// user injected directly (authentication is not what these tests exercise).
func newRevenueFixture(t *testing.T, a *AppEngine, pool *pgxpool.Pool) *revenueFixture {
	t.Helper()
	ctx := context.Background()

	suffix := time.Now().Format("150405000000000")
	email := "rev-cust-" + suffix + "@test.congopro.local"
	cust, err := customers.CreateOrGetByEmail(ctx, pool, email)
	if err != nil {
		t.Fatal(err)
	}
	companyID := "rev-co-" + suffix
	if _, err := pool.Exec(ctx,
		`INSERT INTO companies (id, name, name_seo, status, claimed_by_customer_id)
		 VALUES ($1, 'Revenue SARL', $1, 'published', $2)`, companyID, cust.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO promotions (company_id, customer_id, stripe_subscription_id, status, current_period_end)
		 VALUES ($1, $2, $3, 'active', now() + interval '30 days')`,
		companyID, cust.ID, "sub_rev_"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM promotions WHERE company_id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust.ID)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/revenue", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), constants.StaffUserKey,
			&auth.User{ID: "00000000-0000-0000-0000-000000000000", Name: "Staff", Email: "staff@test", Role: "super_admin"}))
		a.AdminRevenueHandler(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &revenueFixture{srv: srv, email: email}
}

func getRevenuePage(t *testing.T, srv *httptest.Server) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/admin/revenue")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func revenuePool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

const stripeWarnText = "Stripe injoignable"

func TestAdminRevenue_LiveAmounts(t *testing.T) {
	pool := revenuePool(t)
	a := &AppEngine{DB: pool}
	f := newRevenueFixture(t, a, pool)
	// The fixture's sub id embeds a timestamp; the fake answers any id set,
	// so serve a map keyed by lookup from the DB row.
	var subID string
	if err := pool.QueryRow(context.Background(),
		`SELECT p.stripe_subscription_id FROM promotions p JOIN customers cu ON cu.id = p.customer_id WHERE cu.email = $1`,
		f.email).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	a.StripeSubs = &fakeSubReader{amounts: map[string]SubAmount{
		subID: {Amount: 1500, Currency: "usd", Interval: "month"},
	}}

	status, body := getRevenuePage(t, f.srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	for _, want := range []string{"Revenue SARL", f.email, "15.00 $ / mois"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, stripeWarnText) {
		t.Error("warning banner shown although Stripe answered")
	}
}

func TestAdminRevenue_StripeDownDegrades(t *testing.T) {
	pool := revenuePool(t)
	a := &AppEngine{DB: pool, StripeSubs: &fakeSubReader{err: errors.New("boom")}}
	f := newRevenueFixture(t, a, pool)

	status, body := getRevenuePage(t, f.srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded render, never a failed page)", status)
	}
	if !strings.Contains(body, stripeWarnText) {
		t.Error("degraded render missing the warning banner")
	}
	if !strings.Contains(body, "Revenue SARL") {
		t.Error("local promotion data missing from degraded render")
	}
}

func TestAdminRevenue_StripeDisabledDegrades(t *testing.T) {
	pool := revenuePool(t)
	a := &AppEngine{DB: pool} // StripeSubs nil — Stripe not configured
	f := newRevenueFixture(t, a, pool)

	status, body := getRevenuePage(t, f.srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(body, stripeWarnText) {
		t.Error("nil StripeSubs should render the warning banner")
	}
}
