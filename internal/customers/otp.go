package customers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// CodeTTL is how long an issued code stays valid.
	CodeTTL = 10 * time.Minute
	// ResendCooldown throttles re-issuing for the same email.
	ResendCooldown = 60 * time.Second
	// MaxAttempts kills a code after too many wrong verifications.
	MaxAttempts = 5
)

var (
	// ErrInvalidCode is the ONLY error surfaced for verification problems —
	// callers must not distinguish wrong / expired / exhausted / unknown
	// email, or the flow becomes an oracle.
	ErrInvalidCode = errors.New("code invalide ou expiré")
	// ErrCooldown: a code was sent seconds ago; retry later.
	ErrCooldown = errors.New("un code vient d'être envoyé")
)

// GenerateCode returns a 6-digit numeric code with uniform digits
// (crypto/rand, no modulo bias).
func GenerateCode() (string, error) {
	digits := make([]byte, 6)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("customers: generate code: %w", err)
		}
		digits[i] = byte('0' + n.Int64())
	}
	return string(digits), nil
}

// HashCode is the at-rest form of a code. Unsalted SHA-256 is deliberate:
// a 6-digit space cannot resist offline brute force with or without a salt —
// the defenses are the 10-minute TTL, the 5-attempt cap, and the fact that
// the hash lives only in this table.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// IssueCode creates a fresh code for email, superseding any previous one
// (only the newest is ever verifiable), enforcing the resend cooldown, and
// opportunistically purging stale rows so no cron is needed. It returns the
// PLAIN code — the only place it exists outside the email.
func IssueCode(ctx context.Context, db *pgxpool.Pool, email string) (string, error) {
	var last time.Time
	err := db.QueryRow(ctx, `
		SELECT created_at FROM otp_codes
		WHERE email = $1 AND consumed_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, email,
	).Scan(&last)
	if err == nil && time.Since(last) < ResendCooldown {
		return "", ErrCooldown
	}

	code, err := GenerateCode()
	if err != nil {
		return "", err
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	// Supersede older codes and purge anything stale (consumed or expired
	// beyond any debugging interest) — both cheap, both piggybacked here so
	// the table never needs a janitor.
	if _, err := tx.Exec(ctx, `UPDATE otp_codes SET consumed_at = now()
		WHERE email = $1 AND consumed_at IS NULL`, email); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM otp_codes WHERE created_at < now() - interval '1 day'`); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO otp_codes (email, code_hash, expires_at)
		VALUES ($1, $2, now() + $3::interval)`, email, HashCode(code), CodeTTL.String()); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return code, nil
}

// VerifyCode checks a submitted code against the newest unconsumed one,
// enforcing expiry and the attempt cap. The final consume is an atomic
// compare-and-set so a code can never be used twice, even concurrently.
func VerifyCode(ctx context.Context, db *pgxpool.Pool, email, code string) error {
	var (
		id        string
		codeHash  string
		expiresAt time.Time
		attempts  int
	)
	err := db.QueryRow(ctx, `
		SELECT id, code_hash, expires_at, attempts FROM otp_codes
		WHERE email = $1 AND consumed_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, email,
	).Scan(&id, &codeHash, &expiresAt, &attempts)
	if err != nil {
		return ErrInvalidCode
	}

	if time.Now().After(expiresAt) || attempts >= MaxAttempts {
		db.Exec(ctx, `UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, id)
		return ErrInvalidCode
	}

	if HashCode(code) != codeHash {
		db.Exec(ctx, `UPDATE otp_codes SET attempts = attempts + 1
			WHERE id = $1 AND attempts < $2`, id, MaxAttempts)
		return ErrInvalidCode
	}

	// Atomic single-use: only one concurrent verifier wins.
	tag, err := db.Exec(ctx, `UPDATE otp_codes SET consumed_at = now()
		WHERE id = $1 AND consumed_at IS NULL`, id)
	if err != nil || tag.RowsAffected() == 0 {
		return ErrInvalidCode
	}
	return nil
}
