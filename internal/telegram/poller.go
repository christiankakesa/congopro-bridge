package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

// Update / Message / CallbackQuery — only the fields the bot needs.

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

// User is a Telegram account — distinct from auth.User, which it maps to
// via users.telegram_user_id.
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"` // original text + message_id, for the outcome edit
	Data    string   `json:"data"`
}

// Handler processes one update; it runs on the poller goroutine, so a slow
// handler delays subsequent updates — acceptable at staff-chat volume.
type Handler func(ctx context.Context, u Update)

const pollTimeoutSec = 50

// Poll long-polls getUpdates until ctx is cancelled; cancellation aborts
// the in-flight HTTP request immediately, so shutdown never waits out the
// poll timeout. Errors back off linearly 2s,4s,… capped at 30s (the
// LoadAndIndex pattern), reset on success.
//
// The offset lives in memory only: a restart may redeliver the last
// update, so handlers must be idempotent — claims.ErrAlreadyResolved
// provides that for claim callbacks, and command re-runs are harmless.
func Poll(ctx context.Context, c *Client, handle Handler) {
	var offset int64
	fails := 0
	for ctx.Err() == nil {
		updates, err := c.GetUpdates(ctx, offset, pollTimeoutSec)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 409 {
				log.Warn().Msg("[telegram] getUpdates 409 Conflict — another process is consuming this bot token (a dev run against the prod token?). Use a separate dev bot; see .env.template.")
			} else {
				log.Warn().Msgf("[telegram] getUpdates: %v", err)
			}
			fails++
			wait := time.Duration(fails) * 2 * time.Second
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
			continue
		}
		fails = 0
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			handle(ctx, u)
		}
	}
}
