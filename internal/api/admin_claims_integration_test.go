//go:build integration

// Run with: make dev-test-integration

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/auth"
	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/constants"
	"congopro-bridge/internal/customers"
	"congopro-bridge/internal/mail"
)

// blockingMailer holds Send until the test releases it, so "did the handler
// wait for the email?" is answered deterministically rather than by timing.
type blockingMailer struct {
	release   chan struct{}
	sent      chan mail.Message
	releaseOn sync.Once
}

// unblock is idempotent so a cleanup can always call it: if the send ever
// becomes synchronous again, the handler is parked on <-release and would
// otherwise keep httptest.Server.Close() waiting until the whole test binary
// times out, burying the actual failure two minutes later.
func (m *blockingMailer) unblock() { m.releaseOn.Do(func() { close(m.release) }) }

func (m *blockingMailer) Send(msg mail.Message) error {
	<-m.release
	m.sent <- msg
	return nil
}

// Resolving a claim must not block on the decision email: OVH SMTP takes
// ~2-3s to accept one, and the outcome doesn't depend on it. If the send ever
// becomes synchronous again, this test hangs on the request instead of
// returning — which is exactly the regression.
func TestResolveClaimDoesNotWaitForEmail(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL not set — run via make dev-test-integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	mailer := &blockingMailer{release: make(chan struct{}), sent: make(chan mail.Message, 1)}
	a := &AppEngine{DB: pool, Mailer: mailer, MailEnabled: true}

	// A real staff row: resolved_by is a uuid FK, so the handler needs a
	// genuine user in context rather than an empty id.
	suffixStaff := time.Now().Format("150405000000000")
	var staffID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name, role, status, password_hash, totp_secret)
		 VALUES ($1, 'Test Staff', 'super_admin', 'active', 'x', 'x') RETURNING id`,
		"staff-"+suffixStaff+"@test.congopro.local").Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, staffID) })

	mux := http.NewServeMux()
	// deliberately without RequireStaffAuth: this exercises the email timing,
	// not authentication — the staff user is injected directly.
	mux.HandleFunc("POST /admin/claims/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), constants.StaffUserKey, &auth.User{ID: staffID, Email: "staff@test", Role: "super_admin"}))
		a.AdminClaimApproveHandler(w, r)
	})
	srv := httptest.NewServer(mux)
	// Order matters: cleanups run LIFO, so unblock must be registered LAST to
	// run FIRST. srv.Close() waits for outstanding requests, and on the
	// regression this test exists to catch there is a handler parked in
	// Send — closing before unblocking deadlocks until the whole test binary
	// times out, burying the real failure message.
	t.Cleanup(srv.Close)
	t.Cleanup(mailer.unblock)

	suffix := time.Now().Format("150405000000000")
	companyID := "claim-co-" + suffix
	email := "claimant-" + suffix + "@test.congopro.local"
	if _, err := pool.Exec(ctx,
		`INSERT INTO companies (id, name, name_seo, status) VALUES ($1, 'Claim SARL', $1, 'published')`,
		companyID); err != nil {
		t.Fatal(err)
	}
	cust, err := customers.CreateOrGetByEmail(ctx, pool, email)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM company_claims WHERE company_id = $1`, companyID)
		pool.Exec(ctx, `UPDATE companies SET claimed_by_customer_id = NULL WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
		pool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, cust.ID)
	})
	if err := claims.Submit(ctx, pool, companyID, cust.ID, email, "", "owner", "Je suis le gérant de cette entreprise, test d'intégration."); err != nil {
		t.Fatal(err)
	}
	var claimID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM company_claims WHERE company_id = $1`, companyID).Scan(&claimID); err != nil {
		t.Fatal(err)
	}

	// The mailer is still blocked here. A synchronous send would leave this
	// request outstanding until the deadline below fires.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/claims/"+claimID+"/approve",
		strings.NewReader("note=ok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request did not return while the mailer was blocked — the decision email is synchronous again: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 — body: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	// The decision is committed before the response, even though the mail isn't sent.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM company_claims WHERE id = $1`, claimID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Errorf("claim status = %q, want approved", status)
	}

	// ...and the email still goes out once the mailer is released.
	mailer.unblock()
	select {
	case msg := <-mailer.sent:
		if !strings.Contains(msg.To, "claimant-") {
			t.Errorf("unexpected recipient %q", msg.To)
		}
	case <-time.After(5 * time.Second):
		t.Error("decision email was never sent")
	}
}
