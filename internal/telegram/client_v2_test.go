package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rawCapture records the raw JSON body per method path.
type rawCapture struct {
	path string
	body map[string]any
}

func captureServer(t *testing.T, reply string) (*httptest.Server, *rawCapture) {
	t.Helper()
	cap := &rawCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path = r.URL.Path
		cap.body = map[string]any{}
		json.NewDecoder(r.Body).Decode(&cap.body)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func TestSendMessage_KeyboardOnTheWire(t *testing.T) {
	srv, cap := captureServer(t, `{"ok":true}`)
	c := NewForTest("123:t", "-10042", srv.URL)

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "✅", CallbackData: "clm:a:xyz"},
	}}}
	if err := c.SendMessage(context.Background(), "hello", SendOptions{Keyboard: kb}); err != nil {
		t.Fatal(err)
	}
	rm, ok := cap.body["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("reply_markup missing: %v", cap.body)
	}
	rows := rm["inline_keyboard"].([]any)
	btn := rows[0].([]any)[0].(map[string]any)
	if btn["callback_data"] != "clm:a:xyz" {
		t.Errorf("callback_data = %v", btn["callback_data"])
	}
}

// The v1 wire shape must not change: no options ⇒ no reply_markup key at all.
func TestSendMessage_NoOptionsOmitsReplyMarkup(t *testing.T) {
	srv, cap := captureServer(t, `{"ok":true}`)
	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.Send(context.Background(), "plain"); err != nil {
		t.Fatal(err)
	}
	if _, present := cap.body["reply_markup"]; present {
		t.Error("reply_markup present on a plain send")
	}
}

func TestAnswerCallbackQuery(t *testing.T) {
	srv, cap := captureServer(t, `{"ok":true}`)
	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.AnswerCallbackQuery(context.Background(), "cbid-7", "Compte non lié", true); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/bot123:t/answerCallbackQuery" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.body["callback_query_id"] != "cbid-7" || cap.body["show_alert"] != true {
		t.Errorf("body = %v", cap.body)
	}
}

func TestEditMessageText_NilKeyboardOmitted(t *testing.T) {
	srv, cap := captureServer(t, `{"ok":true}`)
	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.EditMessageText(context.Background(), 42, "done", nil); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/bot123:t/editMessageText" {
		t.Errorf("path = %q", cap.path)
	}
	if _, present := cap.body["reply_markup"]; present {
		t.Error("nil keyboard must omit reply_markup (that's how buttons are removed)")
	}
	if cap.body["message_id"] != float64(42) {
		t.Errorf("message_id = %v", cap.body["message_id"])
	}
}

// A double-tap edits a message to text it already has; Telegram answers 400
// "message is not modified". That's success for us.
func TestEditMessageText_NotModifiedSwallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(apiResponse{OK: false, Description: "Bad Request: message is not modified"})
	}))
	defer srv.Close()
	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.EditMessageText(context.Background(), 42, "same", nil); err != nil {
		t.Fatalf("'message is not modified' must be swallowed, got %v", err)
	}
}

func TestGetUpdates_RequestShapeAndDecode(t *testing.T) {
	srv, cap := captureServer(t,
		`{"ok":true,"result":[{"update_id":9,"callback_query":{"id":"cb1","from":{"id":777},"data":"clm:a:u1","message":{"message_id":5,"chat":{"id":-10042},"text":"orig"}}}]}`)
	c := NewForTest("123:t", "-10042", srv.URL)

	updates, err := c.GetUpdates(context.Background(), 3, 50)
	if err != nil {
		t.Fatal(err)
	}
	if cap.body["offset"] != float64(3) || cap.body["timeout"] != float64(50) {
		t.Errorf("request = %v", cap.body)
	}
	allowed := cap.body["allowed_updates"].([]any)
	if len(allowed) != 2 || allowed[0] != "callback_query" || allowed[1] != "message" {
		t.Errorf("allowed_updates = %v", allowed)
	}
	if len(updates) != 1 || updates[0].UpdateID != 9 {
		t.Fatalf("updates = %+v", updates)
	}
	cq := updates[0].CallbackQuery
	if cq == nil || cq.From.ID != 777 || cq.Data != "clm:a:u1" ||
		cq.Message == nil || cq.Message.Chat.ID != -10042 || cq.Message.Text != "orig" {
		t.Errorf("callback = %+v", cq)
	}
}

func TestGetUpdates_409SurfacesAsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(apiResponse{OK: false, Description: "Conflict: terminated by other getUpdates request"})
	}))
	defer srv.Close()
	c := NewForTest("123:t", "-10042", srv.URL)

	_, err := c.GetUpdates(context.Background(), 0, 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("want *APIError{409}, got %v", err)
	}
	if !strings.Contains(apiErr.Description, "other getUpdates") {
		t.Errorf("description = %q", apiErr.Description)
	}
}

// The 15s short-call timeout would abort every quiet 50s long poll — the
// dedicated poll client is load-bearing. Pin both configurations.
func TestConstructor_SeparatePollClient(t *testing.T) {
	c := New("123:t", "-10042")
	short := c.http.Transport.(*http.Transport).ResponseHeaderTimeout
	long := c.pollHTTP.Transport.(*http.Transport).ResponseHeaderTimeout
	if short != 15*time.Second {
		t.Errorf("short-call timeout = %v, want 15s", short)
	}
	if long <= time.Duration(pollTimeoutSec)*time.Second {
		t.Errorf("poll timeout %v must exceed the %ds long poll", long, pollTimeoutSec)
	}
}
