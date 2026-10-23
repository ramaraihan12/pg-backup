package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramSends(t *testing.T) {
	var gotPath, gotChat, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		r.ParseForm()
		gotChat, gotText = r.PostForm.Get("chat_id"), r.PostForm.Get("text")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := &Telegram{Token: "123:abc", ChatID: "-100", BaseURL: srv.URL}
	if err := tg.Notify(context.Background(), "backup failed"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/bot123:abc/sendMessage" || gotChat != "-100" || gotText != "backup failed" {
		t.Errorf("path=%q chat=%q text=%q", gotPath, gotChat, gotText)
	}
}

func TestTelegramErrorsHideTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token 123:abc", http.StatusUnauthorized)
	}))
	url := srv.URL
	tg := &Telegram{Token: "123:abc", ChatID: "1", BaseURL: url}
	err := tg.Notify(context.Background(), "x")
	srv.Close()
	if err == nil || strings.Contains(err.Error(), "123:abc") {
		t.Fatalf("want an error without the token, got %v", err)
	}

	// Connection refused errors include the URL, which includes the token.
	err = tg.Notify(context.Background(), "x")
	if err == nil || strings.Contains(err.Error(), "123:abc") {
		t.Fatalf("want an error without the token, got %v", err)
	}
}

func TestNop(t *testing.T) {
	if err := (Nop{}).Notify(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}
