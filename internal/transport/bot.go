package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PollTimeout is the getUpdates long-poll timeout in seconds. Each request
// gets this plus RequestSlack before it is abandoned.
const PollTimeout = 20

// RequestSlack bounds a request beyond its long-poll time: TLS, proxy and
// server latency.
var RequestSlack = 15 * time.Second

// AllowedUpdates is sent with every getUpdates call. Telegram remembers the
// last value a client sent, so leaving it out can silently keep a stale
// filter (for example one that drops direct messages).
var AllowedUpdates = []string{"message", "channel_post", "my_chat_member"}

// Bot is a minimal Telegram Bot API client: getUpdates long-poll,
// sendMessage and the few setup calls a serving node makes.
type Bot struct {
	Token    string
	BaseURL  string // default https://api.telegram.org; tests point it at a fake
	Username string // filled by GetMe

	offset int64
	hc     *http.Client
	fails  int
	sleep  func(time.Duration) // nil: time.Sleep; tests stub it
}

// NewBot returns a client on an HTTP/1.1-only transport. HTTP/2 streams
// were seen to hang behind egress proxies, stalling the long poll; a
// fresh HTTP/1.1 connection per failure recovers reliably.
func NewBot(token string) *Bot {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.MaxIdleConnsPerHost = 2
	tr.IdleConnTimeout = 60 * time.Second
	return &Bot{Token: token, hc: &http.Client{Transport: tr}}
}

// RetryAfterError is a Telegram 429 with its retry_after hint.
type RetryAfterError struct {
	Method string
	After  time.Duration
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("tg %s: too many requests, retry after %s", e.Method, e.After)
}

func (b *Bot) redact(s string) string {
	if b.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, b.Token, "<token>")
}

func (b *Bot) api(method string, params any, timeout time.Duration) ([]byte, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	base := b.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New(b.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.hc.Do(req)
	if err != nil {
		// url.Error embeds the request URL, which contains the token.
		if inner := errors.Unwrap(err); inner != nil {
			err = inner
		}
		return nil, fmt.Errorf("tg %s: %s", method, b.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tg %s: %s", method, b.redact(err.Error()))
	}
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("tg %s: HTTP %d: undecodable reply", method, resp.StatusCode)
	}
	if !env.OK {
		if env.Parameters.RetryAfter > 0 {
			return nil, &RetryAfterError{method, time.Duration(env.Parameters.RetryAfter) * time.Second}
		}
		return nil, fmt.Errorf("tg %s: %s", method, b.redact(env.Description))
	}
	return env.Result, nil
}

// Chat is the subset of a Telegram chat the node uses.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"` // private, group, supergroup, channel
	Username string `json:"username,omitempty"`
}

type Msg struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// Update is one getUpdates item. Edited messages are not requested
// (see AllowedUpdates) and are ignored if they arrive.
type Update struct {
	UpdateID     int64           `json:"update_id"`
	Message      *Msg            `json:"message"`
	ChannelPost  *Msg            `json:"channel_post"`
	MyChatMember json.RawMessage `json:"my_chat_member,omitempty"`
}

// Poll long-polls getUpdates. After a failure it drops idle connections
// and backs off (1 s doubling to 30 s, with jitter, or Telegram's
// retry_after) before returning the error.
func (b *Bot) Poll(timeoutSec int) ([]Update, error) {
	raw, err := b.api("getUpdates", map[string]any{
		"offset": b.offset, "timeout": timeoutSec, "allowed_updates": AllowedUpdates,
	}, time.Duration(timeoutSec)*time.Second+RequestSlack)
	if err != nil {
		b.backoff(err)
		return nil, err
	}
	var ups []Update
	if err := json.Unmarshal(raw, &ups); err != nil {
		b.backoff(err)
		return nil, err
	}
	b.fails = 0
	for _, u := range ups {
		if u.UpdateID >= b.offset {
			b.offset = u.UpdateID + 1
		}
	}
	return ups, nil
}

func (b *Bot) backoff(err error) {
	b.hc.CloseIdleConnections()
	b.fails++
	d := time.Second << min(b.fails-1, 5)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	d = d/2 + time.Duration(mrand.Int63n(int64(d))) // 0.5x to 1.5x
	var ra *RetryAfterError
	if errors.As(err, &ra) && ra.After > d {
		d = ra.After
	}
	sleep := b.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sleep(d)
}

func (b *Bot) Send(chat, text string) error {
	_, err := b.api("sendMessage", map[string]any{"chat_id": chat, "text": text}, RequestSlack)
	return err
}

// Button is an inline keyboard URL button.
type Button struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// Reply is a formatted bot reply.
type Reply struct {
	HTML       string     // parse_mode HTML; untrusted text must be escaped
	Buttons    [][]Button // inline keyboard rows
	ForceReply string     // non-empty: ask the client to reply, with this placeholder
	ReplyTo    int64      // message to reply to (groups)
}

// SendReply sends r without link previews.
func (b *Bot) SendReply(chat string, r Reply) error {
	p := map[string]any{
		"chat_id": chat, "text": r.HTML, "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	switch {
	case len(r.Buttons) > 0:
		p["reply_markup"] = map[string]any{"inline_keyboard": r.Buttons}
	case r.ForceReply != "":
		p["reply_markup"] = map[string]any{"force_reply": true, "input_field_placeholder": r.ForceReply}
	}
	if r.ReplyTo != 0 {
		p["reply_parameters"] = map[string]any{"message_id": r.ReplyTo, "allow_sending_without_reply": true}
	}
	_, err := b.api("sendMessage", p, RequestSlack)
	return err
}

// GetMe fills b.Username.
func (b *Bot) GetMe() error {
	raw, err := b.api("getMe", map[string]any{}, RequestSlack)
	if err != nil {
		return err
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return err
	}
	b.Username = me.Username
	return nil
}

// BotCommand is one entry of the bot's command menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetupMenu publishes cmds for the default and all-private-chats scopes
// and sets the menu button to show them.
func (b *Bot) SetupMenu(cmds []BotCommand) error {
	var errs []error
	for _, scope := range []string{"default", "all_private_chats"} {
		if _, err := b.api("setMyCommands", map[string]any{
			"commands": cmds, "scope": map[string]any{"type": scope},
		}, RequestSlack); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := b.api("setChatMenuButton", map[string]any{
		"menu_button": map[string]any{"type": "commands"},
	}, RequestSlack); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// logLimiter prints a repeated identical message at most once per window,
// then reports how many repeats it held back.
type logLimiter struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	last   map[string]time.Time
	held   map[string]int
	printf func(string, ...any)
}

func newLogLimiter(printf func(string, ...any)) *logLimiter {
	return &logLimiter{window: time.Minute, now: time.Now, last: map[string]time.Time{},
		held: map[string]int{}, printf: printf}
}

func (l *logLimiter) Printf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if t, ok := l.last[msg]; ok && now.Sub(t) < l.window {
		l.held[msg]++
		return
	}
	if len(l.last) > 1000 {
		l.last, l.held = map[string]time.Time{}, map[string]int{}
	}
	if n := l.held[msg]; n > 0 {
		msg += fmt.Sprintf(" (repeated %d more times)", n)
	}
	l.printf("%s", msg)
	l.last[fmt.Sprintf(format, args...)] = now
	delete(l.held, fmt.Sprintf(format, args...))
}
