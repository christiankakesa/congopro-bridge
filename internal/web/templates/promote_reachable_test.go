package templates

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/data"
)

// /account/promote was unreachable from anywhere in the UI: the only mention
// of it in the whole template tree was the form action on the page itself.
// An owner whose claim was approved had to type the URL by hand, which meant
// the paid feature could not be bought.
func TestPromotePageIsReachable(t *testing.T) {
	render := func(c interface {
		Render(context.Context, io.Writer) error
	}) string {
		var b strings.Builder
		if err := c.Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	cust := &customers.Customer{Email: "owner@example.cd"}

	approved := render(AccountDashboard("N", cust, []claims.Claim{
		{CompanyName: "Congopro", Status: "approved", CreatedAt: time.Now()},
	}))
	if !strings.Contains(approved, `href="/account/promote"`) {
		t.Error("dashboard with an approved claim must link to the promote page")
	}
	if !strings.Contains(approved, "Mettre en avant") {
		t.Error("approved claim should carry the promote CTA")
	}

	// reachable even before anything is approved, so the page is never orphaned
	empty := render(AccountDashboard("N", cust, nil))
	if !strings.Contains(empty, `href="/account/promote"`) {
		t.Error("dashboard must link to the offer even with no claims yet")
	}
}

// "Réclamer" is a dead action once a company has an approved owner — the
// endpoint refuses with ErrAlreadyClaimed.
func TestClaimButtonHiddenOnceVerified(t *testing.T) {
	var b strings.Builder
	c := data.Company{Name: "Congopro", NameSeo: "congopro"}
	if err := CompanyPage("C", "https://x/", "N", CSSVersion, &c, false, true).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "/account/claim") {
		t.Error("verified company should not offer the claim action")
	}

	var b2 strings.Builder
	if err := CompanyPage("C", "https://x/", "N", CSSVersion, &c, false, false).Render(context.Background(), &b2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b2.String(), "/account/claim") {
		t.Error("unclaimed company must still offer the claim action")
	}
}
