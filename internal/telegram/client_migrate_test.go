package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const migrateReply = `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1001234567890}}`

// A supergroup migration must be transparent: the send lands in the new
// chat on the retry, and the client keeps using it afterwards.
func TestSendMessage_AdoptsMigratedChatAndRetries(t *testing.T) {
	var chatIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sendMessageRequest
		json.NewDecoder(r.Body).Decode(&body)
		chatIDs = append(chatIDs, body.ChatID)
		if body.ChatID == "-10042" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(migrateReply))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.Send(context.Background(), "first"); err != nil {
		t.Fatalf("send across migration: %v", err)
	}
	if err := c.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	want := []string{"-10042", "-1001234567890", "-1001234567890"}
	if strings.Join(chatIDs, ",") != strings.Join(want, ",") {
		t.Errorf("chat ids on the wire = %v, want %v", chatIDs, want)
	}
	if got := c.ChatID(); got != -1001234567890 {
		t.Errorf("ChatID() = %d after migration", got)
	}
}

// Same contract for message edits (the poller's outcome edit).
func TestEditMessageText_AdoptsMigratedChatAndRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body editMessageTextRequest
		json.NewDecoder(r.Body).Decode(&body)
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(migrateReply))
			return
		}
		if body.ChatID != "-1001234567890" || body.MessageID != 7 {
			t.Errorf("retry body = %+v", body)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.EditMessageText(context.Background(), 7, "done", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (original + retry)", calls)
	}
}

// If the new chat rejects us too, the error must still name the id and
// the fix — that is what an operator reads in journalctl.
func TestSendMessage_MigrationErrorNamesNewChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(migrateReply))
	}))
	defer srv.Close()

	err := NewForTest("123:t", "-10042", srv.URL).Send(context.Background(), "x")
	if err == nil {
		t.Fatal("want error when the migrated chat also fails")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.MigrateToChatID != -1001234567890 {
		t.Fatalf("error %v does not carry MigrateToChatID", err)
	}
	if !strings.Contains(err.Error(), "TELEGRAM_CHAT_ID=-1001234567890") {
		t.Errorf("error %q does not name the fix", err)
	}
}

// Other 400s must not trigger a retry.
func TestSendMessage_PlainAPIErrorNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer srv.Close()

	c := NewForTest("123:t", "-10042", srv.URL)
	if err := c.Send(context.Background(), "x"); err == nil {
		t.Fatal("want error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if got := c.ChatID(); got != -10042 {
		t.Errorf("ChatID() = %d, must stay configured", got)
	}
}

// Connection errors embed the request URL; the bot token in it must never
// reach the journal, while the underlying error stays matchable.
func TestCall_RedactsTokenFromConnectionErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // dead endpoint

	const token = "8622654738:AAHsecretsecretsecret"
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled ctx fails fast with the URL in the message
	err := NewForTest(token, "1", srv.URL).Send(ctx, "x")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "<token>") {
		t.Errorf("expected redaction marker in %q", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(context.Canceled) lost through redaction: %v", err)
	}
}
