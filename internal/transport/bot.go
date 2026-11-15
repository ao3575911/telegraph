package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Minimal Bot API client: getUpdates long-poll + sendMessage. Nothing else.
type Bot struct {
	Token   string
	BaseURL string // default https://api.telegram.org; tests point it at a fake
	offset  int64
	hc      *http.Client
}

func NewBot(token string) *Bot {
	return &Bot{Token: token, hc: &http.Client{Timeout: 40 * time.Second}}
}

func (b *Bot) api(method string, params any) ([]byte, error) {
	body, _ := json.Marshal(params)
	base := b.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	resp, err := b.hc.Post(base+"/bot"+b.Token+"/"+method,
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK {
		return nil, fmt.Errorf("tg %s: %s", method, env.Description)
	}
	return env.Result, nil
}

type Msg struct {
	MessageID int64 `json:"message_id"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"` // private, group, supergroup, channel
	} `json:"chat"`
	Text string `json:"text"`
}

type Update struct {
	UpdateID    int64 `json:"update_id"`
	Message     *Msg  `json:"message"`
	ChannelPost *Msg  `json:"channel_post"`
}

func (b *Bot) Poll(timeoutSec int) ([]Update, error) {
	raw, err := b.api("getUpdates", map[string]any{
		"offset": b.offset, "timeout": timeoutSec,
	})
	if err != nil {
		return nil, err
	}
	var ups []Update
	if err := json.Unmarshal(raw, &ups); err != nil {
		return nil, err
	}
	for _, u := range ups {
		if u.UpdateID >= b.offset {
			b.offset = u.UpdateID + 1
		}
	}
	return ups, nil
}

func (b *Bot) Send(chat, text string) error {
	_, err := b.api("sendMessage", map[string]any{"chat_id": chat, "text": text})
	return err
}

// SendHTML sends an HTML-formatted message (parse_mode HTML) without
// link previews. Callers escape any untrusted text.
func (b *Bot) SendHTML(chat, text string) error {
	_, err := b.api("sendMessage", map[string]any{
		"chat_id": chat, "text": text, "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	})
	return err
}
