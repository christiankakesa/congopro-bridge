// Package telegram connects the app to a private staff chat via the Bot
// API: outbound notifications (v1) and, since v2, inline-keyboard quick
// actions and chat commands received through getUpdates long-polling
// (poller.go). Still no webhook — nothing here listens on the network.
//
// House rule: this package NEVER logs above Warn, and every log message
// carries the "[telegram]" prefix. That is the loop-protection contract
// with the zerolog error hook (hook.go), which forwards ErrorLevel+ to
// Telegram but skips anything marked "[telegram]" — a failed send (or a
// failing poll loop) can therefore never re-enter the hook.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Notifier is the seam v1 handlers depend on (precedent: mail.Mailer) —
// production wires *Client, tests wire a capture fake.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// InlineKeyboardButton / InlineKeyboardMarkup — only the fields the bot
// uses. CallbackData is capped at 64 bytes by Telegram.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// SendOptions extends plain sends without touching Notifier — v1 call
// sites and fakes keep compiling. The zero value is exactly v1 behaviour.
type SendOptions struct {
	Keyboard *InlineKeyboardMarkup
}

// APIError is a non-OK Bot API reply. The poller matches StatusCode == 409
// (another consumer holds this token's getUpdates) via errors.As.
//
// MigrateToChatID is non-zero when Telegram answered "group chat was
// upgraded to a supergroup chat": the configured chat id is dead for good
// and this is its replacement. Sends adopt it automatically (see
// SendMessage); it is kept on the error so the message names the fix.
type APIError struct {
	StatusCode      int
	Description     string
	MigrateToChatID int64
}

func (e *APIError) Error() string {
	if e.MigrateToChatID != 0 {
		return fmt.Sprintf("HTTP %d: %s — chat migrated to %d, set TELEGRAM_CHAT_ID=%d",
			e.StatusCode, e.Description, e.MigrateToChatID, e.MigrateToChatID)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Description)
}

// redactedError replaces an error's text (which for *url.Error embeds the
// full request URL, bot token included) while keeping the chain intact for
// errors.Is/As — the poller's ctx checks and callers' APIError matches.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redact hides the bot token in err's text. Journald keeps logs for months;
// a token in a timeout message is a token to rotate.
func (c *Client) redact(err error) error {
	if err == nil || c.token == "" {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, c.token) {
		return err
	}
	return &redactedError{msg: strings.ReplaceAll(msg, c.token, "<token>"), err: err}
}

// Client talks to one bot + one chat. Messages are plain text, no
// parse_mode: MarkdownV2 would require escaping 18 characters in
// user-supplied company names and subjects, and bare URLs auto-link
// anyway, which is all the deep links need.
type Client struct {
	token   string
	baseURL string
	// chatID is the configured staff chat until Telegram reports a
	// supergroup migration, after which it is the new id for the life of
	// the process. mu guards it: handlers send from request goroutines
	// while the poller edits messages on its own.
	mu     sync.RWMutex
	chatID string
	// http serves the short calls (sendMessage & co). pollHTTP exists only
	// for GetUpdates: a long poll holds the response open for up to
	// pollTimeoutSec, which the 15s ResponseHeaderTimeout here would abort
	// on every quiet cycle — the bot would look randomly deaf.
	http     *http.Client
	pollHTTP *http.Client
}

const defaultBaseURL = "https://api.telegram.org"

// New builds a client for the given bot token and chat id.
func New(token, chatID string) *Client {
	return &Client{
		token:   token,
		chatID:  chatID,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout: 15 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          2,
			},
		},
		pollHTTP: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout: (pollTimeoutSec + 15) * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          2,
			},
		},
	}
}

// NewForTest points the client at a test server instead of api.telegram.org.
func NewForTest(token, chatID, baseURL string) *Client {
	c := New(token, chatID)
	c.baseURL = baseURL
	return c
}

var _ Notifier = (*Client)(nil)

// ChatID returns the chat currently in use as an integer — the configured
// one, or the supergroup Telegram migrated it to. The bot handler compares
// incoming updates against it so quick actions keep working after a
// migration without a restart.
func (c *Client) ChatID() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, _ := strconv.ParseInt(c.chatID, 10, 64) // shape enforced by config.ValidateTelegram
	return id
}

func (c *Client) currentChat() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chatID
}

// adoptMigration switches to the supergroup id named in err, if any, and
// reports whether the caller should retry. The swap happens once: a
// concurrent send that lost the race finds the id already updated and
// simply retries too. The warning is the operator's cue — the process
// heals itself, the EnvironmentFile does not.
func (c *Client) adoptMigration(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.MigrateToChatID == 0 {
		return false
	}
	newID := strconv.FormatInt(apiErr.MigrateToChatID, 10)
	c.mu.Lock()
	changed := c.chatID != newID
	c.chatID = newID
	c.mu.Unlock()
	if changed {
		log.Warn().Msgf("[telegram] staff chat was upgraded to a supergroup — now sending to %s for this process; make it permanent with: make prod-secrets-set KEY=TELEGRAM_CHAT_ID (value %s) && make prod-app-restart", newID, newID)
	}
	return true
}

type sendMessageRequest struct {
	ChatID             string                `json:"chat_id"`
	Text               string                `json:"text"`
	LinkPreviewOptions linkPreviewOptions    `json:"link_preview_options"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type editMessageTextRequest struct {
	ChatID             string                `json:"chat_id"`
	MessageID          int64                 `json:"message_id"`
	Text               string                `json:"text"`
	LinkPreviewOptions linkPreviewOptions    `json:"link_preview_options"`
	ReplyMarkup        *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type getUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
	Parameters  struct {
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// call POSTs one Bot API method. result may be nil when the caller only
// cares about ok/error; a non-OK reply comes back as *APIError.
func (c *Client) call(ctx context.Context, httpc *http.Client, method string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram connection: %w", c.redact(err))
	}
	defer resp.Body.Close()

	var out apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return fmt.Errorf("telegram decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if !out.OK {
		return &APIError{
			StatusCode:      resp.StatusCode,
			Description:     out.Description,
			MigrateToChatID: out.Parameters.MigrateToChatID,
		}
	}
	if result != nil {
		if err := json.Unmarshal(out.Result, result); err != nil {
			return fmt.Errorf("telegram decode result: %w", err)
		}
	}
	return nil
}

// Send posts one plain-text message to the configured chat (the Notifier
// contract — v1 behaviour, byte for byte).
func (c *Client) Send(ctx context.Context, text string) error {
	return c.SendMessage(ctx, text, SendOptions{})
}

// SendMessage posts one message, optionally with an inline keyboard. A
// supergroup-migration reply switches the client to the new chat id and
// retries once, so a group upgrade costs nothing but a warning.
func (c *Client) SendMessage(ctx context.Context, text string, opts SendOptions) error {
	send := func() error {
		return c.call(ctx, c.http, "sendMessage", sendMessageRequest{
			ChatID:             c.currentChat(),
			Text:               text,
			LinkPreviewOptions: linkPreviewOptions{IsDisabled: true},
			ReplyMarkup:        opts.Keyboard,
		}, nil)
	}
	err := send()
	if c.adoptMigration(err) {
		err = send()
	}
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return fmt.Errorf("telegram sendMessage: %w", err)
		}
		return err
	}
	return nil
}

// AnswerCallbackQuery acknowledges a button tap: a small toast, or a modal
// alert when showAlert is set (used for the "account not linked" guidance).
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string, showAlert bool) error {
	err := c.call(ctx, c.http, "answerCallbackQuery", answerCallbackQueryRequest{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       showAlert,
	}, nil)
	if err != nil {
		return fmt.Errorf("telegram answerCallbackQuery: %w", err)
	}
	return nil
}

// EditMessageText rewrites a message in the configured chat. A nil
// keyboard omits reply_markup, which removes any buttons — the double-tap
// guard. Telegram answers 400 "message is not modified" when nothing
// would change (a double-tap on already-final text); that is a success
// for our purposes and is swallowed here.
func (c *Client) EditMessageText(ctx context.Context, messageID int64, text string, keyboard *InlineKeyboardMarkup) error {
	edit := func() error {
		return c.call(ctx, c.http, "editMessageText", editMessageTextRequest{
			ChatID:             c.currentChat(),
			MessageID:          messageID,
			Text:               text,
			LinkPreviewOptions: linkPreviewOptions{IsDisabled: true},
			ReplyMarkup:        keyboard,
		}, nil)
	}
	err := edit()
	if c.adoptMigration(err) {
		err = edit()
	}
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && strings.Contains(apiErr.Description, "message is not modified") {
			return nil
		}
		return fmt.Errorf("telegram editMessageText: %w", err)
	}
	return nil
}

// GetUpdates long-polls for updates. Uses the dedicated pollHTTP client —
// see the Client struct comment. The passed ctx aborts the in-flight
// request immediately on cancellation, which is how shutdown avoids
// waiting out the poll timeout.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, c.pollHTTP, "getUpdates", getUpdatesRequest{
		Offset:         offset,
		Timeout:        timeoutSec,
		AllowedUpdates: []string{"callback_query", "message"},
	}, &updates)
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: %w", err)
	}
	return updates, nil
}
