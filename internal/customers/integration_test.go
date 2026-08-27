//go:build integration

// Integration tests for the customer account flow — the repo's first
// integration-tagged tests. Run with `make dev-test-integration` (starts local
// Postgres, applies migrations, sets DATABASE_URL).
package customers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		// Soft-skip: someone ran the tag without the env (make handles it).
		os.Exit(0)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		panic(err)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// uniqueEmail gives every test its own identity — no cross-test cleanup
// choreography needed.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	return t.Name() + "-" + time.Now().Format("150405.000000000") + "@test.congopro.local"
}

func cleanupEmail(t *testing.T, email string) {
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM customers WHERE email = $1`, email)
		testPool.Exec(context.Background(), `DELETE FROM otp_codes WHERE email = $1`, email)
	})
}

func TestOTP_IssueAndVerify(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	code, err := IssueCode(ctx, testPool, email)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	if err := VerifyCode(ctx, testPool, email, code); err != nil {
		t.Fatalf("VerifyCode with the issued code: %v", err)
	}
	// Single use: the same code must not work twice.
	if err := VerifyCode(ctx, testPool, email, code); err != ErrInvalidCode {
		t.Fatalf("reused code: got %v, want ErrInvalidCode", err)
	}
}

func TestOTP_Cooldown(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	if _, err := IssueCode(ctx, testPool, email); err != nil {
		t.Fatalf("first IssueCode: %v", err)
	}
	if _, err := IssueCode(ctx, testPool, email); err != ErrCooldown {
		t.Fatalf("second IssueCode within cooldown: got %v, want ErrCooldown", err)
	}
}

func TestOTP_WrongCodeExhausts(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	code, err := IssueCode(ctx, testPool, email)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	for i := 0; i < MaxAttempts; i++ {
		if err := VerifyCode(ctx, testPool, email, "000000"); err != ErrInvalidCode {
			t.Fatalf("wrong code attempt %d: got %v, want ErrInvalidCode", i, err)
		}
	}
	// Attempts exhausted: even the RIGHT code is now dead.
	if err := VerifyCode(ctx, testPool, email, code); err != ErrInvalidCode {
		t.Fatalf("right code after exhaustion: got %v, want ErrInvalidCode", err)
	}
}

func TestOTP_NewCodeSupersedesOld(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	old, err := IssueCode(ctx, testPool, email)
	if err != nil {
		t.Fatalf("first IssueCode: %v", err)
	}
	// Bypass the cooldown for the test by backdating the first code.
	if _, err := testPool.Exec(ctx,
		`UPDATE otp_codes SET created_at = created_at - interval '2 minutes' WHERE email = $1`, email); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	fresh, err := IssueCode(ctx, testPool, email)
	if err != nil {
		t.Fatalf("second IssueCode: %v", err)
	}
	if err := VerifyCode(ctx, testPool, email, old); err != ErrInvalidCode {
		t.Fatal("superseded code must not verify")
	}
	if err := VerifyCode(ctx, testPool, email, fresh); err != nil {
		t.Fatalf("newest code must verify: %v", err)
	}
}

func TestOTP_UnknownEmailIsGenericInvalid(t *testing.T) {
	if err := VerifyCode(context.Background(), testPool, uniqueEmail(t), "123456"); err != ErrInvalidCode {
		t.Fatalf("got %v, want ErrInvalidCode (never reveal unknown email)", err)
	}
}

func TestCustomers_CreateOrGetByEmail(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	c1, err := CreateOrGetByEmail(ctx, testPool, email)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c2, err := CreateOrGetByEmail(ctx, testPool, email)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if c1.ID != c2.ID {
		t.Fatalf("second login created a second account: %s vs %s", c1.ID, c2.ID)
	}
	// Emails are canonicalized to lowercase on storage.
	if want := strings.ToLower(email); c2.Email != want {
		t.Fatalf("stored email = %q, want %q", c2.Email, want)
	}

	// Disabled accounts are rejected on login.
	if _, err := testPool.Exec(ctx, `UPDATE customers SET status = 'disabled' WHERE id = $1`, c1.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := CreateOrGetByEmail(ctx, testPool, email); err != ErrCustomerDisabled {
		t.Fatalf("disabled login: got %v, want ErrCustomerDisabled", err)
	}
}

func TestSessions_Roundtrip(t *testing.T) {
	ctx := context.Background()
	email := uniqueEmail(t)
	cleanupEmail(t, email)

	cust, err := CreateOrGetByEmail(ctx, testPool, email)
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	token, expiresAt, err := CreateSession(ctx, testPool, cust.ID, "test-agent", "127.0.0.1:1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if time.Until(expiresAt) < 29*24*time.Hour {
		t.Errorf("session TTL too short: expires %s", expiresAt)
	}

	got, err := SessionCustomer(ctx, testPool, token)
	if err != nil {
		t.Fatalf("SessionCustomer: %v", err)
	}
	if got.Email != strings.ToLower(email) {
		t.Fatalf("session resolved to %q", got.Email)
	}

	if err := RevokeSession(ctx, testPool, token); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := SessionCustomer(ctx, testPool, token); err != ErrSessionNotFound {
		t.Fatalf("revoked session: got %v, want ErrSessionNotFound", err)
	}
}
