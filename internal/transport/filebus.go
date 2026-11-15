package transport

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Conn is a message bus the Syncer gossips over. *Bot (Telegram) and
// *FileBus (a local file, for tests and demos) implement it.
type Conn interface {
	Send(chat, text string) error
	Poll(timeoutSec int) ([]Update, error)
}

// FileBus is a local stand-in for a Telegram channel: an append-only
// JSON-lines file shared by every node on one machine. Each node keeps
// its read offset in OffsetPath so it sees each message once.
type FileBus struct {
	Path       string
	OffsetPath string

	mu     sync.Mutex
	offset int64
	nextID int64
}

type busLine struct {
	Chat string `json:"c"`
	Text string `json:"t"`
}

// OpenFileBus opens (creating if needed) the bus at path, resuming from
// the offset saved in offsetPath ("" keeps the offset in memory only).
func OpenFileBus(path, offsetPath string) (*FileBus, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	b := &FileBus{Path: path, OffsetPath: offsetPath}
	if offsetPath != "" {
		raw, err := os.ReadFile(offsetPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(raw) > 0 {
			if b.offset, err = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}

// Send appends one message. Each message is a single write on an
// O_APPEND descriptor, so concurrent local writers do not interleave.
func (b *FileBus) Send(chat, text string) error {
	line, err := json.Marshal(busLine{Chat: chat, Text: text})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(b.Path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Poll returns messages appended since the last poll. When there are
// none it waits briefly (at most min(timeoutSec, 0.2s)) and returns.
func (b *FileBus) Poll(timeoutSec int) ([]Update, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ups, err := b.read()
	if err != nil || len(ups) > 0 || timeoutSec <= 0 {
		return ups, err
	}
	time.Sleep(200 * time.Millisecond)
	return b.read()
}

func (b *FileBus) read() ([]Update, error) {
	f, err := os.Open(b.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(b.offset, io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	var ups []Update
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF or a partial trailing line: retry next poll
		}
		b.offset += int64(len(raw))
		var l busLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		b.nextID++
		ups = append(ups, Update{UpdateID: b.nextID, ChannelPost: &Msg{Text: l.Text}})
	}
	if b.OffsetPath != "" && len(ups) > 0 {
		if err := os.WriteFile(b.OffsetPath, []byte(strconv.FormatInt(b.offset, 10)), 0o600); err != nil {
			return ups, err
		}
	}
	return ups, nil
}
