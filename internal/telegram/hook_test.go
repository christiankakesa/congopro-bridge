package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// chanNotifier collects sends on a channel so tests can wait on the async
// worker with a timeout instead of sleeping.
type chanNotifier struct {
	sent chan string
}

func (n *chanNotifier) Send(_ context.Context, text string) error {
	n.sent <- text
	return nil
}

func waitMsg(t *testing.T, n *chanNotifier) string {
	t.Helper()
	select {
	case msg := <-n.sent:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("no message forwarded within 2s")
		return ""
	}
}

func TestHook_ForwardsErrorNotInfo(t *testing.T) {
	n := &chanNotifier{sent: make(chan string, 8)}
	h := NewErrorHook(n, 10)

	h.Run(nil, zerolog.InfoLevel, "just info")
	h.Run(nil, zerolog.WarnLevel, "just warn")
	h.Run(nil, zerolog.ErrorLevel, "database exploded")

	msg := waitMsg(t, n)
	if !strings.Contains(msg, "database exploded") {
		t.Errorf("forwarded %q, want the error text", msg)
	}
	select {
	case extra := <-n.sent:
		t.Errorf("info/warn leaked through: %q", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHook_SkipsTelegramMarkedMessages(t *testing.T) {
	n := &chanNotifier{sent: make(chan string, 8)}
	h := NewErrorHook(n, 10)

	// The loop guard: a telegram-send failure logged with the marker must
	// never be forwarded back to Telegram.
	h.Run(nil, zerolog.ErrorLevel, "[telegram] sendMessage: HTTP 502")
	h.Run(nil, zerolog.ErrorLevel, "real error")

	if msg := waitMsg(t, n); strings.Contains(msg, "[telegram]") {
		t.Errorf("marked message forwarded: %q", msg)
	}
}

func TestHook_SkipsClientDisconnects(t *testing.T) {
	n := &chanNotifier{sent: make(chan string, 8)}
	h := NewErrorHook(n, 10)

	// A visitor leaving mid-request cancels r.Context(); the resulting
	// errors are logged locally but must not page the staff chat. Both the
	// bare form and pgx's wrapped form carry the context.Canceled text.
	h.Run(nil, zerolog.ErrorLevel, "[search] promoted lookup: context canceled")
	h.Run(nil, zerolog.ErrorLevel, "[templates] render company page \"acme\": timeout: context already done: context canceled")
	// A deadline is a slow query, not a departed client — it must go through.
	h.Run(nil, zerolog.ErrorLevel, "[search] promoted lookup: context deadline exceeded")

	if msg := waitMsg(t, n); !strings.Contains(msg, "deadline exceeded") {
		t.Errorf("got %q, want the deadline error", msg)
	}
	select {
	case extra := <-n.sent:
		t.Errorf("client-disconnect error forwarded: %q", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHook_RateLimitCountsDrops(t *testing.T) {
	n := &chanNotifier{sent: make(chan string, 32)}
	h := NewErrorHook(n, 2)

	for i := 0; i < 5; i++ {
		h.Run(nil, zerolog.ErrorLevel, "boom")
	}
	// 2 admitted, 3 dropped by the rate limit. The counter is flushed on
	// whichever delivery the worker reaches after the drops happen, so
	// collect every message and assert the total appears somewhere.
	msgs := []string{waitMsg(t, n), waitMsg(t, n)}

	// Force a new window so one more message is admitted — by now the
	// dropped counter is guaranteed non-zero if it was never flushed.
	h.mu.Lock()
	h.windowStart = time.Now().Add(-2 * time.Minute)
	h.mu.Unlock()
	h.Run(nil, zerolog.ErrorLevel, "after the storm")
	msgs = append(msgs, waitMsg(t, n))

	reported := false
	for _, m := range msgs {
		if strings.Contains(m, "erreurs supprimées") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("drop count never reported across %q", msgs)
	}
}

// A wedged notifier must never wedge logging: Run always returns
// immediately, even with the worker stuck and the buffer full.
func TestHook_NeverBlocksLogging(t *testing.T) {
	block := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(block) })

	blocked := blockingNotifier{block: block}
	h := NewErrorHook(&blocked, 100000)

	done := make(chan struct{})
	go func() {
		for i := 0; i < hookQueueSize*3; i++ {
			h.Run(nil, zerolog.ErrorLevel, "flood")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked on a full queue")
	}
}

type blockingNotifier struct{ block chan struct{} }

func (b *blockingNotifier) Send(context.Context, string) error {
	<-b.block
	return nil
}
