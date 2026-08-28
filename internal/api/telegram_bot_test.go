package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"congopro-bridge/internal/telegram"
)

func TestParseClaimCallback(t *testing.T) {
	cases := []struct {
		data          string
		action, claim string
		ok            bool
	}{
		{"clm:a:0192d3e4-uuid", "a", "0192d3e4-uuid", true},
		{"clm:r:xyz", "r", "xyz", true},
		{"clm:x:xyz", "", "", false}, // unknown action
		{"clm:a:", "", "", false},    // empty id
		{"other:a:xyz", "", "", false},
		{"", "", "", false},
		{"clm:a", "", "", false}, // no second separator
	}
	for _, tc := range cases {
		action, claim, ok := parseClaimCallback(tc.data)
		if action != tc.action || claim != tc.claim || ok != tc.ok {
			t.Errorf("parseClaimCallback(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.data, action, claim, ok, tc.action, tc.claim, tc.ok)
		}
	}
}

// Telegram rejects callback_data over 64 bytes — with a 36-char uuid the
// payload must stay well under.
func TestClaimKeyboard_DataUnder64Bytes(t *testing.T) {
	kb := claimKeyboard("0192d3e4-89ab-7cde-b012-3456789abcde")
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("keyboard shape = %+v", kb)
	}
	for _, btn := range kb.InlineKeyboard[0] {
		if n := len(btn.CallbackData); n == 0 || n > 64 {
			t.Errorf("callback_data %q is %d bytes", btn.CallbackData, n)
		}
	}
}

// fakeResponder records rich-side calls. (Unique name — the api test
// package is shared across tagged and untagged files.)
type fakeResponder struct {
	mu       sync.Mutex
	sends    []string
	sendOpts []telegram.SendOptions
	answers  []string
	alerts   []bool
	edits    []string
	editKBs  []*telegram.InlineKeyboardMarkup
	editIDs  []int64
}

func (f *fakeResponder) SendMessage(_ context.Context, text string, opts telegram.SendOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, text)
	f.sendOpts = append(f.sendOpts, opts)
	return nil
}

func (f *fakeResponder) AnswerCallbackQuery(_ context.Context, _, text string, alert bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	f.alerts = append(f.alerts, alert)
	return nil
}

func (f *fakeResponder) EditMessageText(_ context.Context, messageID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, text)
	f.editKBs = append(f.editKBs, kb)
	f.editIDs = append(f.editIDs, messageID)
	return nil
}

func (f *fakeResponder) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends) + len(f.answers) + len(f.edits)
}

func TestNotifyTelegramNewClaim_SendsKeyboard(t *testing.T) {
	fr := &fakeResponder{}
	a := &AppEngine{TelegramBot: fr}
	a.notifyTelegramNewClaim("Congopro SARL", "cust@x.cd", "https://congopro.com", "claim-1")

	if len(fr.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(fr.sends))
	}
	if !strings.Contains(fr.sends[0], "Nouvelle réclamation — Congopro SARL") {
		t.Errorf("text = %q", fr.sends[0])
	}
	kb := fr.sendOpts[0].Keyboard
	if kb == nil || kb.InlineKeyboard[0][0].CallbackData != "clm:a:claim-1" {
		t.Errorf("keyboard = %+v", kb)
	}
}

// nil TelegramBot must fall back to the plain v1 Notifier — no buttons,
// but the notification still goes out.
func TestNotifyTelegramNewClaim_FallsBackToNotifier(t *testing.T) {
	n := newCaptureNotifier()
	a := &AppEngine{Telegram: n}
	a.notifyTelegramNewClaim("Congopro SARL", "cust@x.cd", "https://congopro.com", "claim-1")

	msg := n.waitOne(t)
	if !strings.Contains(msg, "Nouvelle réclamation — Congopro SARL") {
		t.Errorf("fallback message = %q", msg)
	}
}

// Anything from a chat other than the configured one is dropped before a
// single query runs — nil DB proves the chat check comes first (a query
// would panic).
func TestHandleUpdate_WrongChatIgnoredBeforeAnyQuery(t *testing.T) {
	fr := &fakeResponder{}
	h := &TelegramHandler{App: &AppEngine{}, Resp: fr, ChatID: -10042}

	h.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID:      "cb1",
		From:    telegram.User{ID: 7},
		Data:    "clm:a:xyz",
		Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: -999}, Text: "orig"},
	}})
	h.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
		MessageID: 2,
		From:      &telegram.User{ID: 7},
		Chat:      telegram.Chat{ID: 12345}, // a DM
		Text:      "/pending",
	}})
	// A callback without an attached message (e.g. too-old message) too.
	h.HandleUpdate(context.Background(), telegram.Update{CallbackQuery: &telegram.CallbackQuery{
		ID:   "cb2",
		From: telegram.User{ID: 7},
		Data: "clm:a:xyz",
	}})

	// The handler is synchronous; a tiny settle keeps this future-proof.
	time.Sleep(20 * time.Millisecond)
	if n := fr.totalCalls(); n != 0 {
		t.Fatalf("responder called %d times for foreign-chat updates, want 0", n)
	}
}

// Non-command chatter in the staff chat must stay unanswered.
func TestHandleUpdate_OrdinaryChatterIgnored(t *testing.T) {
	fr := &fakeResponder{}
	h := &TelegramHandler{App: &AppEngine{}, Resp: fr, ChatID: -10042}

	for _, text := range []string{"bonjour à tous", "/unknown", "", "pending"} {
		h.HandleUpdate(context.Background(), telegram.Update{Message: &telegram.Message{
			MessageID: 3,
			From:      &telegram.User{ID: 7},
			Chat:      telegram.Chat{ID: -10042},
			Text:      text,
		}})
	}
	if n := fr.totalCalls(); n != 0 {
		t.Fatalf("responder called %d times for chatter, want 0", n)
	}
}
