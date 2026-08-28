package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoll_DeliversAndAdvancesOffset(t *testing.T) {
	var calls atomic.Int64
	var secondOffset atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var req getUpdatesRequest
		json.NewDecoder(r.Body).Decode(&req)
		if n == 1 {
			w.Write([]byte(`{"ok":true,"result":[{"update_id":41,"message":{"message_id":1,"chat":{"id":-1},"text":"/pending"}}]}`))
			return
		}
		secondOffset.Store(req.Offset)
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	got := make(chan Update, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Poll(ctx, NewForTest("123:t", "-1", srv.URL), func(_ context.Context, u Update) {
			select {
			case got <- u:
			default:
			}
		})
	}()

	select {
	case u := <-got:
		if u.UpdateID != 41 || u.Message == nil || u.Message.Text != "/pending" {
			t.Errorf("update = %+v", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no update delivered")
	}
	// Wait until at least one follow-up request carried the advanced offset.
	deadline := time.Now().Add(2 * time.Second)
	for secondOffset.Load() != 42 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if secondOffset.Load() != 42 {
		t.Errorf("second request offset = %d, want 42 (update_id+1)", secondOffset.Load())
	}
}

// Cancellation must abort the in-flight long poll immediately — shutdown
// can never sit behind the 50s timeout.
func TestPoll_CancelAbortsInFlightRequest(t *testing.T) {
	inFlight := make(chan struct{})
	// release lets the handler return so srv.Close() can complete: the
	// server does not reliably observe the client's abort (the handler
	// never reads the body, so close-detection may not fire) — and the
	// assertion below only concerns the CLIENT side anyway.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inFlight)
		select { // hold the response open like a real long poll
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release) // LIFO: releases the handler before srv.Close waits on it

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Poll(ctx, NewForTest("123:t", "-1", srv.URL), func(context.Context, Update) {})
	}()

	<-inFlight
	start := time.Now()
	cancel()
	select {
	case <-done:
		if d := time.Since(start); d > time.Second {
			t.Errorf("Poll took %v to stop after cancel", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Poll did not return after cancel")
	}
}

// An error response must not kill the loop: back off, then keep polling.
func TestPoll_RecoversAfterError(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"ok":false,"description":"boom"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"chat":{"id":-1},"text":"hi"}}]}`))
	}))
	defer srv.Close()

	got := make(chan Update, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Poll(ctx, NewForTest("123:t", "-1", srv.URL), func(_ context.Context, u Update) {
		select {
		case got <- u:
		default:
		}
	})

	select {
	case <-got: // reached the handler despite the first-request failure
	case <-time.After(5 * time.Second): // must outlast the first 2s backoff
		t.Fatal("poller never recovered after an error response")
	}
}
