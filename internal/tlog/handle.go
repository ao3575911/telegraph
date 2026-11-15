package tlog

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxHandleLen is the longest handle, in characters.
const MaxHandleLen = 64

// ErrBadHandle: a handle must be name@namespace with no spaces.
var ErrBadHandle = errors.New("handle must look like name@nym: one @, no spaces, at most 64 characters")

// ValidHandle is the handle rule shared by the CLI and the bot: at most
// MaxHandleLen characters, exactly one @ with text on both sides, and no
// whitespace.
func ValidHandle(h string) error {
	if utf8.RuneCountInString(h) > MaxHandleLen || strings.Count(h, "@") != 1 ||
		strings.ContainsFunc(h, unicode.IsSpace) {
		return ErrBadHandle
	}
	at := strings.IndexByte(h, '@')
	if at == 0 || at == len(h)-1 {
		return ErrBadHandle
	}
	return nil
}
