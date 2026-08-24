// Package customers implements customer accounts: passwordless, verified by
// one-time email codes. The verified email IS the identity — there is no
// password and no separate signup step. It mirrors internal/auth's shape
// (package-level functions taking an explicit pool, sentinel errors) but
// shares nothing with it: staff and customers have different trust models.
package customers

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Customer struct {
	ID     string
	Email  string
	Name   string
	Status string
}

var (
	ErrCustomerDisabled = errors.New("customer account is disabled")
	ErrNotFound         = errors.New("customer not found")
)

// emailPattern is deliberately conservative — a valid RFC 5322 address is not
// the goal, rejecting obvious junk before it hits the OTP mail pipeline is.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail trims, lowercases, and validates. The customers_email_key
// index is functional on lower(email), so one canonical form everywhere.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if len(e) < 3 || len(e) > 254 || !emailPattern.MatchString(e) {
		return "", errors.New("adresse email invalide")
	}
	return e, nil
}

// CreateOrGetByEmail returns the customer for email, creating the account on
// first contact. Called ONLY after a successful OTP verification — the
// mailbox has been proven by then; code requests never create rows.
// The email is canonicalized to lowercase here so callers can't introduce a
// case-variant duplicate.
func CreateOrGetByEmail(ctx context.Context, db *pgxpool.Pool, email string) (*Customer, error) {
	var c Customer
	err := db.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO customers (email)
			VALUES (lower($1))
			ON CONFLICT (lower(email)) DO NOTHING
			RETURNING id, email, name, status
		)
		SELECT id, email, name, status FROM ins
		UNION ALL
		SELECT id, email, name, status FROM customers WHERE lower(email) = lower($1)
		LIMIT 1`, email,
	).Scan(&c.ID, &c.Email, &c.Name, &c.Status)
	if err != nil {
		return nil, err
	}
	if c.Status != "active" {
		return nil, ErrCustomerDisabled
	}
	return &c, nil
}

// TouchLogin records the login time; failure is non-fatal for the caller.
func TouchLogin(ctx context.Context, db *pgxpool.Pool, id string) {
	db.Exec(ctx, `UPDATE customers SET last_login_at = now(), updated_at = now() WHERE id = $1`, id)
}
