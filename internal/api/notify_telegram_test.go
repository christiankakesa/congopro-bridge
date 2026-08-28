package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// captureNotifier collects Telegram sends on a channel so tests can wait on
// the fire-and-forget goroutine with a timeout. (Unique name — the api test
// package is shared across tagged and untagged files.)
type captureNotifier struct {
	sent chan string
}

func newCaptureNotifier() *captureNotifier {
	return &captureNotifier{sent: make(chan string, 8)}
}

func (c *captureNotifier) Send(_ context.Context, text string) error {
	c.sent <- text
	return nil
}

func (c *captureNotifier) waitOne(t *testing.T) string {
	t.Helper()
	select {
	case msg := <-c.sent:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("no Telegram notification within 2s")
		return ""
	}
}

func (c *captureNotifier) expectNone(t *testing.T) {
	t.Helper()
	select {
	case msg := <-c.sent:
		t.Fatalf("unexpected Telegram notification: %q", msg)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestContact_ValidSubmitNotifiesTelegram(t *testing.T) {
	n := newCaptureNotifier()
	a := &AppEngine{Mailer: &contactMailer{}, MailEnabled: true, ContactTo: "ask@congopro.com", Telegram: n}

	w := postContact(t, a, validForm())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	msg := n.waitOne(t)
	for _, want := range []string{"Message de contact", "Christian", "Correction de fiche"} {
		if !strings.Contains(msg, want) {
			t.Errorf("notification %q missing %q", msg, want)
		}
	}
}

// The honeypot path answers like a success but must stay silent everywhere —
// including the staff chat, or bots would train staff to ignore it.
func TestContact_HoneypotDoesNotNotifyTelegram(t *testing.T) {
	n := newCaptureNotifier()
	a := &AppEngine{Mailer: &contactMailer{}, MailEnabled: true, ContactTo: "ask@congopro.com", Telegram: n}

	form := validForm()
	form.Set("website_url", "https://spam.example")
	w := postContact(t, a, form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (honeypot mimics success)", w.Code)
	}
	n.expectNone(t)
}

// A failed email send means the visitor saw an error and will retry —
// notifying staff would produce duplicates for every retry.
func TestContact_MailFailureDoesNotNotifyTelegram(t *testing.T) {
	n := newCaptureNotifier()
	a := &AppEngine{Mailer: &contactMailer{err: errTest}, MailEnabled: true, ContactTo: "ask@congopro.com", Telegram: n}

	w := postContact(t, a, validForm())
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	n.expectNone(t)
}

// nil Telegram (not configured) must be a silent no-op, not a panic.
func TestNotifyTelegram_NilNotifierIsNoOp(t *testing.T) {
	a := &AppEngine{}
	a.notifyTelegram("anything") // would panic on a nil-deref bug
}

var errTest = errors.New("smtp exploded")
