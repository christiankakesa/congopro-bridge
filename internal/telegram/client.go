// Package telegram sends staff notifications to a private chat via the Bot
// API. Send-only by design (docs/BACKEND_PROPOSAL.md scopes the bot as a
// notification/quick-action layer; quick actions are a later phase): no
// polling, no webhook receiver, no Telegram identities to map.
//
// House rule: this package NEVER logs above Warn, and every log message
// carries the "[telegram]" prefix. That is the loop-protection contract
// with the zerolog error hook (hook.go), which forwards ErrorLevel+ to
// Telegram but skips anything marked "[telegram]" — a failed send can
// therefore never re-enter the hook.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Notifier is the seam handlers depend on (precedent: mail.Mailer) —
// production wires *Client, tests wire a capture fake.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// Client talks to one bot + one chat. Messages are plain text, no
// parse_mode: MarkdownV2 would require escaping 18 characters in
// user-supplied company names and subjects, and bare URLs auto-link
// anyway, which is all the deep links need.
type Client struct {
	token   string
	chatID  string
	baseURL string
	http    *http.Client
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
	}
}

// NewForTest points the client at a test server instead of api.telegram.org.
func NewForTest(token, chatID, baseURL string) *Client {
	c := New(token, chatID)
	c.baseURL = baseURL
	return c
}

var _ Notifier = (*Client)(nil)

type sendMessageRequest struct {
	ChatID             string             `json:"chat_id"`
	Text               string             `json:"text"`
	LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// Send posts one plain-text message to the configured chat.
func (c *Client) Send(ctx context.Context, text string) error {
	body, err := json.Marshal(sendMessageRequest{
		ChatID:             c.chatID,
		Text:               text,
		LinkPreviewOptions: linkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return fmt.Errorf("telegram encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram connection: %w", err)
	}
	defer resp.Body.Close()

	var out apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return fmt.Errorf("telegram decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if !out.OK {
		return fmt.Errorf("telegram sendMessage: HTTP %d: %s", resp.StatusCode, out.Description)
	}
	return nil
}
