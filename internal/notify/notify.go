// Package notify sends failure alerts.
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Notifier delivers a short text message.
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// Nop drops every message. It is what runs when notifications are off.
type Nop struct{}

func (Nop) Notify(context.Context, string) error { return nil }

// Telegram sends messages through the Bot API.
type Telegram struct {
	Token  string
	ChatID string
	// BaseURL defaults to https://api.telegram.org. Tests point it elsewhere.
	BaseURL string
	Client  *http.Client
}

// Notify posts text to the chat. Errors never contain the bot token.
func (t *Telegram) Notify(ctx context.Context, text string) error {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{"chat_id": {t.ChatID}, "text": {text}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/bot"+t.Token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return t.scrub(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return t.scrub(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return t.scrub(fmt.Errorf("telegram answered %s: %s", resp.Status, strings.TrimSpace(string(body))))
	}
	return nil
}

func (t *Telegram) scrub(err error) error {
	if t.Token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.Token, "<token>"))
}
