//go:build integration

// Claims workflow integration tests — run via `make dev-test-integration`.
package claims

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/customers"
)

var claimsPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		// Loud, not silent: a silent exit-0 once made six "passing" runs out
		// of a suite that never executed. Run via make dev-test-integration.
		fmt.Fprintln(os.Stderr, "claims integration tests: DATABASE_URL not set — run via make dev-test-integration")
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		panic(err)
	}
	claimsPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

var uniqCounter atomic.Uint64

// uniq is collision-proof across parallel tests: monotonic counter plus
// nanoseconds. A pure timestamp once produced a duplicate fixture during a
// full-suite run (two fixtures inside the same clock tick), which cost an
// hour of flake-hunting — never again.
func uniq() string {
	return time.Now().Format("150405") + "-" +
		time.Now().Format("000000000") + "-" +
		strconv.FormatUint(uniqCounter.Add(1), 10)
}

// newCustomer + newCompany + newStaff: fixtures with cleanup.
func newCustomer(t *testing.T, label string) *customers.Customer {
	t.Helper()
	email := "cl-" + label + "-" + uniq() + "@test.congopro.local"
	c, err := customers.CreateOrGetByEmail(context.Background(), claimsPool, email)
	if err != nil {
		t.Fatalf("customer fixture: %v", err)
	}
	t.Cleanup(func() {
		claimsPool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, c.ID)
	})
	return c
}

func newCompany(t *testing.T, label string) string {
	t.Helper()
	id := "cl-co-" + label + "-" + uniq()
	_, err := claimsPool.Exec(context.Background(),
		`INSERT INTO companies (id, name, name_seo, status) VALUES ($1, $2, $3, 'published')`,
		id, "Entreprise "+label, id)
	if err != nil {
		t.Fatalf("company fixture: %v", err)
	}
	t.Cleanup(func() {
		claimsPool.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, id)
	})
	return id
}

func newStaff(t *testing.T) string {
	t.Helper()
	hash, _ := auth.HashPassword("test-password-long-enough")
	var id string
	err := claimsPool.QueryRow(context.Background(),
		`INSERT INTO users (email, name, password_hash, role) VALUES ($1, 'Test Staff', $2, 'support') RETURNING id`,
		"staff-"+uniq()+"@test.congopro.local", hash).Scan(&id)
	if err != nil {
		t.Fatalf("staff fixture: %v", err)
	}
	t.Cleanup(func() {
		claimsPool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

const goodEvidence = "Je suis le fondateur de cette entreprise depuis 2015, RCCM disponible sur demande."

func TestClaims_LifecycleApprove(t *testing.T) {
	ctx := context.Background()
	cust := newCustomer(t, "owner")
	other := newCustomer(t, "other")
	company := newCompany(t, "approve")
	staff := newStaff(t)

	// Fresh state: form-eligible.
	st, err := StateForCompany(ctx, claimsPool, company, cust.ID)
	if err != nil || st.Claimed || st.Pending {
		t.Fatalf("fresh state = %+v, %v", st, err)
	}

	// Submit → pending.
	if _, err := Submit(ctx, claimsPool, company, cust.ID, cust.Email, "+243900000001", RelationshipOwner, goodEvidence); err != nil {
		t.Fatalf("submit: %v", err)
	}
	st, _ = StateForCompany(ctx, claimsPool, company, cust.ID)
	if !st.Pending || !st.PendingByMe {
		t.Fatalf("after submit state = %+v", st)
	}

	// Another customer cannot pile on while pending.
	if _, err := Submit(ctx, claimsPool, company, other.ID, other.Email, "", RelationshipOwner, goodEvidence); err != ErrAlreadyPending {
		t.Fatalf("second submit: got %v, want ErrAlreadyPending", err)
	}

	// Admin approves: claim approved + company claimed by the claimant.
	claimantEmail, companyName, err := Approve(ctx, claimsPool, firstClaimID(t, company), staff, "documents vérifiés")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if claimantEmail != cust.Email || companyName == "" {
		t.Fatalf("approve returned %q / %q", claimantEmail, companyName)
	}

	st, _ = StateForCompany(ctx, claimsPool, company, cust.ID)
	if !st.Claimed || !st.ClaimedByMe {
		t.Fatalf("after approve state = %+v", st)
	}

	// Double-approve (stale tab) is a friendly no-op error.
	if _, _, err := Approve(ctx, claimsPool, firstClaimID(t, company), staff, ""); err != ErrAlreadyResolved {
		t.Fatalf("double approve: got %v, want ErrAlreadyResolved", err)
	}

	// New claims on an approved company are refused.
	if _, err := Submit(ctx, claimsPool, company, other.ID, other.Email, "", RelationshipManager, goodEvidence); err != ErrAlreadyClaimed {
		t.Fatalf("submit after approve: got %v, want ErrAlreadyClaimed", err)
	}
}

func TestClaims_LifecycleRejectThenRetry(t *testing.T) {
	ctx := context.Background()
	cust := newCustomer(t, "rejected")
	other := newCustomer(t, "retry")
	company := newCompany(t, "reject")
	staff := newStaff(t)

	if _, err := Submit(ctx, claimsPool, company, cust.ID, cust.Email, "", RelationshipOther, goodEvidence); err != nil {
		t.Fatalf("submit: %v", err)
	}
	claimID := firstClaimID(t, company)

	claimant, name, err := Reject(ctx, claimsPool, claimID, staff, "preuves insuffisantes")
	if err != nil || claimant != cust.Email || name == "" {
		t.Fatalf("reject: %v %q %q", err, claimant, name)
	}

	// Company stays unclaimed; a NEW claim (same or another customer) is fine.
	st, _ := StateForCompany(ctx, claimsPool, company, cust.ID)
	if st.Claimed || st.Pending {
		t.Fatalf("after reject state = %+v", st)
	}
	if _, err := Submit(ctx, claimsPool, company, other.ID, other.Email, "", RelationshipOwner, goodEvidence+" Second essai."); err != nil {
		t.Fatalf("retry after reject: %v", err)
	}

	// Reject sets the admin note for the record.
	var note string
	_ = claimsPool.QueryRow(ctx, `SELECT admin_note FROM company_claims WHERE id = $1`, claimID).Scan(&note)
	if note != "preuves insuffisantes" {
		t.Fatalf("admin_note = %q", note)
	}
}

func TestClaims_Validation(t *testing.T) {
	ctx := context.Background()
	cust := newCustomer(t, "val")
	company := newCompany(t, "val")

	if _, err := Submit(ctx, claimsPool, company, cust.ID, cust.Email, "", "boss", goodEvidence); err != ErrInvalidRelationship {
		t.Fatalf("bad relationship: got %v", err)
	}
	if _, err := Submit(ctx, claimsPool, company, cust.ID, cust.Email, "", RelationshipOwner, "trop court"); err != ErrEvidenceTooShort {
		t.Fatalf("short evidence: got %v", err)
	}
	if _, err := Submit(ctx, claimsPool, "missing-company", cust.ID, cust.Email, "", RelationshipOwner, goodEvidence); err != ErrCompanyNotFound {
		t.Fatalf("missing company: got %v", err)
	}
}

func TestClaims_Lists(t *testing.T) {
	ctx := context.Background()
	cust := newCustomer(t, "list")
	company := newCompany(t, "list")
	staff := newStaff(t)

	if _, err := Submit(ctx, claimsPool, company, cust.ID, cust.Email, "", RelationshipOwner, goodEvidence); err != nil {
		t.Fatal(err)
	}

	mine, err := ListByCustomer(ctx, claimsPool, cust.ID)
	if err != nil || len(mine) != 1 || mine[0].CompanyName == "" || mine[0].CompanyNameSeo == "" {
		t.Fatalf("ListByCustomer = %v, %v", mine, err)
	}

	all, err := ListForAdmin(ctx, claimsPool, "pending")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range all {
		if c.CompanyID == company {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending claim not in admin list (%d rows)", len(all))
	}

	if _, _, err := Approve(ctx, claimsPool, firstClaimID(t, company), staff, ""); err != nil {
		t.Fatal(err)
	}
	approved, _ := ListForAdmin(ctx, claimsPool, "approved")
	found = false
	for _, c := range approved {
		if c.CompanyID == company {
			found = true
		}
	}
	if !found {
		t.Fatal("approved claim missing from approved filter")
	}
}

func firstClaimID(t *testing.T, companyID string) string {
	t.Helper()
	var id string
	err := claimsPool.QueryRow(context.Background(),
		`SELECT id FROM company_claims WHERE company_id = $1 ORDER BY created_at DESC LIMIT 1`, companyID).Scan(&id)
	if err != nil {
		t.Fatalf("claim fixture lookup: %v", err)
	}
	return id
}
