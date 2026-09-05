package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ErrorHook forwards ErrorLevel+ log messages to a Notifier so the staff
// chat sees production errors as they happen.
//
// Four properties are non-negotiable and each has a test:
//   - Logging never blocks: Run pushes into a buffered channel with a
//     non-blocking select; a full buffer drops the message.
//   - No feedback loop: messages containing "[telegram]" are skipped, and
//     this package never logs above Warn — a failed forward cannot re-enter.
//   - No client-disconnect noise: a visitor closing the tab mid-request
//     cancels r.Context(), and every DB call or template render still in
//     flight fails with context.Canceled. Handlers log that at Error like
//     any other failure, which is right for the local log but is not an
//     incident — so the hook drops it before it reaches the staff chat.
//   - Bounded volume: at most maxPerMinute messages reach Telegram; drops
//     are counted and reported on the next delivered message.
type ErrorHook struct {
	queue chan string

	mu          sync.Mutex
	windowStart time.Time
	sentInWin   int
	dropped     int

	maxPerMinute int
}

const hookQueueSize = 64

// NewErrorHook starts the forwarding worker and returns the hook to attach
// with log.Logger.Hook(...). The worker runs for the life of the process —
// an in-flight message lost on shutdown is accepted, same trade-off as the
// async claim email.
func NewErrorHook(n Notifier, maxPerMinute int) *ErrorHook {
	h := &ErrorHook{
		queue:        make(chan string, hookQueueSize),
		maxPerMinute: maxPerMinute,
	}
	go h.worker(n)
	return h
}

// Run implements zerolog.Hook. Called on every log event, so the fast path
// (below Error, or marked) must return immediately.
func (h *ErrorHook) Run(_ *zerolog.Event, level zerolog.Level, msg string) {
	if level < zerolog.ErrorLevel || msg == "" {
		return
	}
	if strings.Contains(msg, "[telegram]") {
		return // the loop guard — see the package comment
	}
	if isClientGone(msg) {
		return // stays in the local log, never pages anyone
	}
	if !h.admit() {
		return
	}
	select {
	case h.queue <- msg:
	default:
		h.noteDrop() // full buffer: drop rather than stall the caller
	}
}

// isClientGone reports whether a message is the trace of a caller that hung
// up: the request context was cancelled, so whatever was in flight (pgx
// query, templ render) returned context.Canceled. Matched on the error text
// because the hook only sees the formatted message. context.DeadlineExceeded
// is deliberately not matched — a slow query is a real problem.
func isClientGone(msg string) bool {
	return strings.Contains(msg, context.Canceled.Error())
}

// admit applies the fixed-window rate limit and counts what it refuses.
func (h *ErrorHook) admit() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if now.Sub(h.windowStart) >= time.Minute {
		h.windowStart = now
		h.sentInWin = 0
	}
	if h.sentInWin >= h.maxPerMinute {
		h.dropped++
		return false
	}
	h.sentInWin++
	return true
}

func (h *ErrorHook) noteDrop() {
	h.mu.Lock()
	h.dropped++
	// The slot reserved by admit goes unused; give it back so a full
	// buffer doesn't also eat the rate budget.
	h.sentInWin--
	h.mu.Unlock()
}

// takeDropped returns and resets the dropped counter.
func (h *ErrorHook) takeDropped() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.dropped
	h.dropped = 0
	return d
}

func (h *ErrorHook) worker(n Notifier) {
	for msg := range h.queue {
		text := "⚠️ Erreur applicative\n" + msg
		if d := h.takeDropped(); d > 0 {
			text += fmt.Sprintf("\n(+%d erreurs supprimées)", d)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := n.Send(ctx, text); err != nil {
			// Warn, never Error — see the package comment.
			log.Warn().Msgf("[telegram] error-forward failed: %v", err)
		}
		cancel()
	}
}
