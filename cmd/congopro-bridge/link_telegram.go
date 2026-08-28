package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/auth"
)

// linkTelegram interactively binds a staff account to a Telegram user id so
// bot quick actions (approve/reject from the chat) are attributed to a real
// person in resolved_by. Run on the server via `make prod-staff-telegram-link`.
// The id to enter is what the bot shows when an unlinked person taps a
// button ("votre identifiant Telegram est …").
func linkTelegram(ctx context.Context, pool *pgxpool.Pool) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Staff email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read email: %w", err)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return fmt.Errorf("email is required")
	}

	user, err := auth.UserByEmail(ctx, pool, email)
	if errors.Is(err, auth.ErrUserNotFound) {
		return fmt.Errorf("no staff account with email %q", email)
	}
	if err != nil {
		return err
	}
	if user.Status != "active" {
		return fmt.Errorf("account %s is disabled — re-enable it before linking", user.Email)
	}

	fmt.Print("Telegram user id: ")
	idStr, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read telegram id: %w", err)
	}
	tgID, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return fmt.Errorf("not a numeric Telegram user id: %q (the bot shows it when an unlinked user taps a button)", strings.TrimSpace(idStr))
	}

	// Report existing state before writing — overwrites are deliberate acts.
	var current *int64
	if err := pool.QueryRow(ctx,
		`SELECT telegram_user_id FROM users WHERE id = $1`, user.ID).Scan(&current); err != nil {
		return fmt.Errorf("read current link: %w", err)
	}
	if current != nil && *current != tgID {
		if !confirm(reader, fmt.Sprintf("⚠ %s is already linked to Telegram id %d — overwrite? [y/N] ", user.Email, *current)) {
			fmt.Println("Aborted, nothing changed.")
			return nil
		}
	}
	if other, err := auth.UserByTelegramID(ctx, pool, tgID); err == nil && other.ID != user.ID {
		if !confirm(reader, fmt.Sprintf("⚠ Telegram id %d is currently linked to %s — move it? [y/N] ", tgID, other.Email)) {
			fmt.Println("Aborted, nothing changed.")
			return nil
		}
	} else if err != nil && !errors.Is(err, auth.ErrUserNotFound) {
		return err
	}

	if err := auth.LinkTelegramID(ctx, pool, user.ID, tgID); err != nil {
		return err
	}
	fmt.Printf("✓ Linked %s (%s) ↔ Telegram id %d — bot actions are now attributed to this account.\n",
		user.Email, user.Role, tgID)
	return nil
}

func confirm(reader *bufio.Reader, prompt string) bool {
	fmt.Print(prompt)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}
