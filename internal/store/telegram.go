package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Telegram user ids are personal data: the store never keeps them, and
// nothing derived from them that could be reversed is gossiped. A node
// refers to a Telegram user by a tag, an HMAC-SHA256 of the id under a
// node-local secret (TELEGRAPH_HOME/telegram.secret, 32 random bytes,
// mode 0600, never gossiped). Ids are small integers, so a plain hash
// could be inverted by enumeration; the secret prevents that, and two
// nodes give the same user unrelated tags.

const secretFile = "telegram.secret"

// TagLen is the length of a tag in hex characters (128 bits).
const TagLen = 32

// TelegramTag returns the tag for Telegram user id. The secret is loaded,
// or created, next to the store on first use; an in-memory store (no
// Path) uses a random per-process secret.
func (s *Store) TelegramTag(id int64) string {
	if s.tgSecret == nil {
		sec, err := loadSecret(s.Path)
		if err != nil {
			// never fall back to a raw id: a fresh random secret only
			// costs custody continuity, which Save would not persist anyway
			sec = randomSecret()
		}
		s.tgSecret = sec
	}
	m := hmac.New(sha256.New, s.tgSecret)
	m.Write([]byte("telegraph/telegram-user/v1/" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(m.Sum(nil))[:TagLen]
}

func randomSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func loadSecret(storePath string) ([]byte, error) {
	if storePath == "" {
		return randomSecret(), nil
	}
	p := filepath.Join(filepath.Dir(storePath), secretFile)
	b, err := os.ReadFile(p)
	if err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("%s: want 32 bytes", p)
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = randomSecret()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) { // created concurrently: use that one
		return loadSecret(storePath)
	}
	if err != nil {
		return nil, err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return b, err
}

// migrateCustody converts custody records from data.json files written
// before v0.3.1, which held raw Telegram user ids, to tags. It runs in
// memory on Open; the raw ids leave the file on the next Save.
func (s *Store) migrateCustody() error {
	if len(s.LegacyCustody) == 0 {
		return nil
	}
	sec, err := loadSecret(s.Path)
	if err != nil {
		return fmt.Errorf("migrating custody: %w", err)
	}
	s.tgSecret = sec
	if s.Custody == nil {
		s.Custody = map[string]string{}
	}
	for h, id := range s.LegacyCustody {
		if _, ok := s.Custody[h]; !ok {
			s.Custody[h] = s.TelegramTag(id)
		}
	}
	s.LegacyCustody = nil
	return nil
}
