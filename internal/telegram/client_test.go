package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientSend(t *testing.T) {
	var gotPath string
	var gotBody sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(apiResponse{OK: true})
	}))
	defer srv.Close()

	c := NewForTest("123:token", "-10042", srv.URL)
	if err := c.Send(context.Background(), "hello staff"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/bot123:token/sendMessage" {
		t.Errorf("path = %q, want the token embedded", gotPath)
	}
	if gotBody.ChatID != "-10042" || gotBody.Text != "hello staff" {
		t.Errorf("body = %+v", gotBody)
	}
	if !gotBody.LinkPreviewOptions.IsDisabled {
		t.Error("link previews must be disabled")
	}
}

func TestClientSend_APIErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(apiResponse{OK: false, Description: "chat not found"})
	}))
	defer srv.Close()

	err := NewForTest("123:t", "1", srv.URL).Send(context.Background(), "x")
	if err == nil {
		t.Fatal("want error on ok:false")
	}
	if want := "chat not found"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q missing %q", err, want)
	}
}

func TestClientSend_ConnectionErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // dead endpoint

	err := NewForTest("123:t", "1", srv.URL).Send(context.Background(), "x")
	if err == nil {
		t.Fatal("want error on dead endpoint")
	}
	if !strings.Contains(err.Error(), "telegram connection") {
		t.Errorf("error %q not wrapped as connection error", err)
	}
}
