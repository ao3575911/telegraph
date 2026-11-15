package handshake

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/ao3575911/telegraph/internal/tlog"
)

const cardPrefix = "⟁ "

// CardLine: the canonical bio line. The tool is the sole printer, so
// uniformity is automatic.
func CardLine(handle string, regEntry *tlog.Entry) string {
	return cardPrefix + handle + " · " + regEntry.HashHex()[:8]
}

// ParseCardLine extracts (handle, first-8-hex) from a card line.
func ParseCardLine(line string) (handle, hex8 string, ok bool) {
	rest := line
	if len(rest) > len(cardPrefix) && rest[:len(cardPrefix)] == cardPrefix {
		rest = rest[len(cardPrefix):]
	}
	sep := " · "
	for i := 0; i+len(sep) <= len(rest); i++ {
		if rest[i:i+len(sep)] == sep {
			h, tail := rest[:i], rest[i+len(sep):]
			if len(h) > 0 && len(tail) >= 8 {
				return h, tail[:8], true
			}
		}
	}
	return "", "", false
}

// BuildCard signs an attestation binding the exact bio line.
func BuildCard(line string, ts int64, priv ed25519.PrivateKey, prevHash []byte) (*tlog.Entry, error) {
	nonce := make([]byte, 8)
	rand.Read(nonce)
	h := sha256.Sum256([]byte(line))
	return tlog.New(tlog.Payload{
		Kind: tlog.KindCard, DID: line, Handle: "card",
		Hash: hex.EncodeToString(h[:]), TS: ts, Nonce: nonce,
	}, priv, prevHash)
}

// VerifyCard: line verified iff a valid Card entry by the current
// Owner(handle) carries exactly this text and hash.
func VerifyCard(line string, handle string, entries []tlog.Entry) bool {
	h := sha256.Sum256([]byte(line))
	want := hex.EncodeToString(h[:])
	pub, _, _ := Owner(handle, entries)
	if pub == nil {
		return false
	}
	for i := range entries {
		e := &entries[i]
		if e.Payload.Kind == tlog.KindCard && e.Payload.Hash == want &&
			e.Payload.DID == line && bytes.Equal(e.PubKey, pub) && e.Verify() == nil {
			return true
		}
	}
	return false
}
