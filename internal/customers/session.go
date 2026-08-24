package customers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// CustomerSessionCookieName is the cookie carrying the session token.
	CustomerSessionCookieName = "congopro_customer_session"
	// customerSessionTTL is longer than the staff 7 days: passwordless
	// login convenience — re-requesting a code is the recovery path.
	customerSessionTTL = 30 * 24 * time.Hour
)

var ErrSessionNotFound = errors.New("customer session not found or expired")

// newSessionToken: 32 bytes of crypto/rand, URL-safe base64 (43 chars).
// Entropy is the defense, same contract as staff sessions.
func newSessionToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("customers: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// CreateSession stores a new session and returns the opaque token plus its
// expiry. Piggybacks a purge of long-expired sessions — cheap, and means no
// janitor process is needed at this scale.
func CreateSession(ctx context.Context, db *pgxpool.Pool, customerID, userAgent, ip string) (token string, expiresAt time.Time, err error) {
	token = newSessionToken()
	expiresAt = time.Now().Add(customerSessionTTL)

	tx, err := db.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM customer_sessions WHERE expires_at < now()`); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO customer_sessions (token, customer_id, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, token, customerID, userAgent, ip, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// SessionCustomer resolves a token to its active customer, or
// ErrSessionNotFound. Disabled accounts fail here too, defense in depth.
func SessionCustomer(ctx context.Context, db *pgxpool.Pool, token string) (*Customer, error) {
	var c Customer
	err := db.QueryRow(ctx, `
		SELECT c.id, c.email, c.name, c.status
		FROM customer_sessions s
		JOIN customers c ON c.id = s.customer_id
		WHERE s.token = $1 AND s.expires_at > now()`, token,
	).Scan(&c.ID, &c.Email, &c.Name, &c.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	if c.Status != "active" {
		return nil, ErrSessionNotFound
	}
	return &c, nil
}

// RevokeSession deletes one session (logout).
func RevokeSession(ctx context.Context, db *pgxpool.Pool, token string) error {
	_, err := db.Exec(ctx, `DELETE FROM customer_sessions WHERE token = $1`, token)
	return err
}
