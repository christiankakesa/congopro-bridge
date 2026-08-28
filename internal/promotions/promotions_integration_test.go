//go:build integration

// Promotions lifecycle integration tests — run via `make dev-test-integration`.
// Webhook events are hand-built JSON applied through the same appliers the
// Stripe webhook handler uses, so no Stripe account is needed.
package promotions

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/customers"
)

var promoPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		// Loud, not silent (see claims tests for why).
		println("promotions integration tests: DATABASE_URL not set — run via make dev-test-integration")
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		panic(err)
	}
	promoPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func fixture(t *testing.T) (companyID, customerID string) {
	t.Helper()
	ctx := context.Background()

	email := "promo-" + time.Now().Format("150405.000000000") + "@test.congopro.local"
	cust, err := customers.CreateOrGetByEmail(ctx, promoPool, email)
	if err != nil {
		t.Fatal(err)
	}
	companyID = "promo-co-" + time.Now().Format("150405.000000000")
	if _, err := promoPool.Exec(ctx,
		`INSERT INTO companies (id, name, name_seo, status) VALUES ($1, 'Promo SARL', $1, 'published')`,
		companyID); err != nil {
		t.Fatal(err)
	}
	// Ownership via the claims-approved column.
	if _, err := promoPool.Exec(ctx,
		`UPDATE companies SET claimed_by_customer_id = $2 WHERE id = $1`, companyID, cust.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		promoPool.Exec(ctx, `DELETE FROM promotions WHERE company_id = $1`, companyID)
		promoPool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
		promoPool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust.ID)
	})
	return companyID, cust.ID
}

func TestPromotions_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	companyID, customerID := fixture(t)

	// Eligible before anything exists.
	el, err := EligibleForCustomer(ctx, promoPool, customerID)
	if err != nil || len(el) != 1 || el[0].ID != companyID {
		t.Fatalf("eligible = %v, %v", el, err)
	}

	// Checkout opens: pending row.
	_, err = CreatePending(ctx, promoPool, companyID, customerID, "cs_test_123")
	if err != nil {
		t.Fatal(err)
	}

	// Second live promo on the same company: refused (index-arbitrated).
	if _, err := CreatePending(ctx, promoPool, companyID, customerID, "cs_test_456"); err != ErrAlreadyPromoted {
		t.Fatalf("duplicate live: got %v", err)
	}

	// Eligibility flips off while pending.
	el, _ = EligibleForCustomer(ctx, promoPool, customerID)
	if len(el) != 0 {
		t.Fatalf("pending promo must remove eligibility, got %d", len(el))
	}

	// checkout.session.completed → active; the first apply is a real
	// transition (drives the activation notification).
	periodEnd := time.Now().AddDate(0, 1, 0).Truncate(time.Second)
	activated, err := ApplyCheckoutCompleted(ctx, promoPool, "cs_test_123", "cus_test_1", "sub_test_1", periodEnd)
	if err != nil {
		t.Fatal(err)
	}
	if !activated {
		t.Fatal("first checkout apply must report activated")
	}

	promoted, err := PromotedCompanyIDs(ctx, promoPool, []string{companyID})
	if err != nil || !promoted[companyID] {
		t.Fatalf("badge query after activation = %v, %v", promoted, err)
	}
	if ok, _ := IsPromoted(ctx, promoPool, companyID); !ok {
		t.Fatal("IsPromoted false after activation")
	}

	// Replay the same event: no state change, no error — and no reported
	// transition, so a replayed webhook can never re-notify.
	activated, err = ApplyCheckoutCompleted(ctx, promoPool, "cs_test_123", "cus_test_1", "sub_test_1", periodEnd)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if activated {
		t.Fatal("replay must not report activated")
	}

	// The admin revenue listing sees the promotion with the customer email
	// joined (the display join is the whole point of AllForAdmin).
	all, err := AllForAdmin(ctx, promoPool)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range all {
		if p.CompanyID == companyID {
			found = true
			if p.CustomerEmail == "" {
				t.Error("AllForAdmin: customer email not joined")
			}
			if p.Status != "active" {
				t.Errorf("AllForAdmin: status = %q, want active", p.Status)
			}
		}
	}
	if !found {
		t.Fatal("AllForAdmin: activated promotion not listed")
	}

	// subscription.updated → past_due; badge still shows (grace). The
	// transition is reported (drives the past-due notification)…
	oldSt, newSt, err := ApplySubscriptionUpdated(ctx, promoPool, "sub_test_1", "past_due", periodEnd)
	if err != nil {
		t.Fatal(err)
	}
	if oldSt != "active" || newSt != "past_due" {
		t.Fatalf("updated transition = %q→%q, want active→past_due", oldSt, newSt)
	}
	// …and a same-status replay reports no transition.
	oldSt, newSt, err = ApplySubscriptionUpdated(ctx, promoPool, "sub_test_1", "past_due", periodEnd)
	if err != nil || oldSt != newSt {
		t.Fatalf("updated replay = %q→%q, %v; want equal statuses", oldSt, newSt, err)
	}
	// Unknown subscription: ignored, empty transition.
	if o, n, err := ApplySubscriptionUpdated(ctx, promoPool, "sub_unknown", "active", periodEnd); err != nil || o != "" || n != "" {
		t.Fatalf("unknown sub = %q→%q, %v; want empty, nil", o, n, err)
	}
	if ok, _ := IsPromoted(ctx, promoPool, companyID); !ok {
		t.Fatal("past_due must remain promoted (grace period semantics)")
	}

	// subscription.deleted → canceled; slot freed. First delete is a real
	// transition, the replay is not.
	canceled, err := ApplySubscriptionDeleted(ctx, promoPool, "sub_test_1")
	if err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Fatal("first delete must report canceled")
	}
	canceled, err = ApplySubscriptionDeleted(ctx, promoPool, "sub_test_1")
	if err != nil || canceled {
		t.Fatalf("delete replay = %v, %v; want false, nil", canceled, err)
	}
	if ok, _ := IsPromoted(ctx, promoPool, companyID); ok {
		t.Fatal("canceled must stop promotion")
	}
	el, _ = EligibleForCustomer(ctx, promoPool, customerID)
	if len(el) != 1 {
		t.Fatal("canceled must restore eligibility")
	}

	// Re-subscribe allowed.
	if _, err := CreatePending(ctx, promoPool, companyID, customerID, "cs_test_789"); err != nil {
		t.Fatalf("re-promote after cancel: %v", err)
	}
}

func TestPromotions_CancelPendingFreesSlot(t *testing.T) {
	ctx := context.Background()
	companyID, customerID := fixture(t)

	promo, err := CreatePending(ctx, promoPool, companyID, customerID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelPending(ctx, promoPool, promo.ID); err != nil {
		t.Fatal(err)
	}
	// Slot free again.
	if _, err := CreatePending(ctx, promoPool, companyID, customerID, "cs_x"); err != nil {
		t.Fatalf("after CancelPending: %v", err)
	}
}

func TestMapSubscriptionStatus(t *testing.T) {
	cases := map[string]string{
		"active": "active", "trialing": "active",
		"past_due": "past_due", "unpaid": "past_due",
		"canceled": "canceled", "incomplete": "canceled", "incomplete_expired": "canceled",
	}
	for in, want := range cases {
		if got := MapSubscriptionStatus(in); got != want {
			t.Errorf("MapSubscriptionStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
