package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID     string
	Email  string
	Name   string
	Role   string
	Status string
}

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrTOTPNotEnrolled    = errors.New("totp not enrolled")
	ErrInvalidTOTPCode    = errors.New("invalid totp code")
)

// dummyHash lets Login run a bcrypt comparison even when no user matches the
// given email, so a nonexistent-email response takes roughly as long as a
// wrong-password one — bcrypt's cost dominates lookup time either way, so
// skipping it on a miss would let an attacker time-distinguish valid emails.
var dummyHash string

func init() {
	// MinPasswordLength-compliant filler; the value itself is never checked
	// against anything, only used to pay bcrypt's cost.
	h, err := HashPassword("dummy-password-for-constant-time-login")
	if err != nil {
		panic("auth: failed to compute dummy hash: " + err.Error())
	}
	dummyHash = h
}

// authUserRow is the internal shape used only for login verification — it
// carries the password hash and TOTP secret, which User (returned to callers
// after successful auth) deliberately does not.
type authUserRow struct {
	User
	PasswordHash string
	TOTPSecret   *string
}

func getUserByEmail(ctx context.Context, db *pgxpool.Pool, email string) (*authUserRow, error) {
	var r authUserRow
	err := db.QueryRow(ctx, `
		SELECT id, email, name, role, status, password_hash, totp_secret
		FROM users WHERE lower(email) = lower($1)
	`, email).Scan(&r.ID, &r.Email, &r.Name, &r.Role, &r.Status, &r.PasswordHash, &r.TOTPSecret)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	return &r, nil
}

// Login verifies email + password + TOTP code, in that order. All failure
// paths return one of a small set of sentinel errors — callers should show a
// generic "invalid credentials" message regardless of which one, except
// ErrTOTPNotEnrolled which is only reachable for an operator-created account
// that hasn't finished enrollment yet.
func Login(ctx context.Context, db *pgxpool.Pool, email, password, totpCode string) (*User, error) {
	r, err := getUserByEmail(ctx, db, email)
	if err != nil {
		_ = VerifyPassword(dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	if r.Status != "active" {
		_ = VerifyPassword(dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	if !VerifyPassword(r.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	if r.TOTPSecret == nil {
		return nil, ErrTOTPNotEnrolled
	}
	if !VerifyTOTPCode(*r.TOTPSecret, totpCode) {
		return nil, ErrInvalidTOTPCode
	}
	if _, err := db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, r.ID); err != nil {
		return nil, fmt.Errorf("update last_login_at: %w", err)
	}
	return &r.User, nil
}

// CreateUser creates a staff account with a freshly generated TOTP secret and
// returns the otpauth:// URI for enrollment — there is no self-service signup,
// so this is only ever called from the -create-admin bootstrap flow today.
func CreateUser(ctx context.Context, db *pgxpool.Pool, email, name, password, role string) (*User, string, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}
	secret, uri, err := GenerateTOTPSecret(email)
	if err != nil {
		return nil, "", err
	}
	var u User
	err = db.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, totp_secret, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, name, role, status
	`, email, name, hash, secret, role).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Status)
	if err != nil {
		return nil, "", fmt.Errorf("insert user: %w", err)
	}
	return &u, uri, nil
}
