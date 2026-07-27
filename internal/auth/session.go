package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SessionCookieName = "congopro_admin_session"
	sessionTTL        = 7 * 24 * time.Hour
)

var ErrSessionNotFound = errors.New("session not found or expired")

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSession issues a new opaque session token for userID and returns it —
// the caller is responsible for setting it as an HttpOnly, Secure cookie.
func CreateSession(ctx context.Context, db *pgxpool.Pool, userID, userAgent, ip string) (token string, expiresAt time.Time, err error) {
	token, err = newSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(sessionTTL)
	_, err = db.Exec(ctx, `
		INSERT INTO sessions (token, user_id, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, token, userID, userAgent, ip, expiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, expiresAt, nil
}

// SessionUser resolves a session token to its user, provided the session
// exists, hasn't expired, and the account is still active. Expired sessions
// are left for a future cleanup pass rather than deleted eagerly here — at
// this scale a handful of stale rows costs nothing.
func SessionUser(ctx context.Context, db *pgxpool.Pool, token string) (*User, error) {
	var u User
	err := db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.role, u.status
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = $1 AND s.expires_at > now()
	`, token).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	if u.Status != "active" {
		return nil, ErrSessionNotFound
	}
	return &u, nil
}

func RevokeSession(ctx context.Context, db *pgxpool.Pool, token string) error {
	_, err := db.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
