package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"congopro-bridge/internal/auth"
)

// createAdmin is an interactive wizard for creating the first staff account.
// There's no self-service signup for staff — this is the only way to get a
// super_admin into the system, run once against a fresh database.
func createAdmin(ctx context.Context, pool *pgxpool.Pool) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read email: %w", err)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}

	fmt.Print("Name: ")
	name, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read name: %w", err)
	}
	name = strings.TrimSpace(name)

	password, err := readPassword(reader, "Password (min 12 characters): ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	confirm, err := readPassword(reader, "Confirm password: ")
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	if password != confirm {
		return fmt.Errorf("passwords do not match")
	}

	user, otpURI, err := auth.CreateUser(ctx, pool, email, name, password, "super_admin")
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("✓ Created super_admin %s (%s)\n", user.Email, user.ID)
	fmt.Println()
	fmt.Println("Scan this in an authenticator app (Google Authenticator, 1Password, etc.),")
	fmt.Println("or enter the otpauth URI manually if your app supports it — it won't be shown again:")
	fmt.Println()
	fmt.Println("  " + otpURI)
	fmt.Println()
	fmt.Println("Log in at /admin/login once TOTP is enrolled.")
	return nil
}

// readPassword masks input on a real terminal. Falls back to a plain
// newline-delimited read when stdin isn't a TTY (e.g. piped input for
// scripted/CI use), since term.ReadPassword requires an actual terminal.
func readPassword(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
