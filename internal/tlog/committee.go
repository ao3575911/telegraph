package tlog

import (
	"errors"
	"sync"
)

// The committee allowlist (TELEGRAPH_COMMITTEE). Without it, any key that
// writes one log entry is an issuer and can be picked for the checkpoint
// committee, so whoever registers a handle can sign checkpoints. With it,
// only the listed keys can sit on a committee: a checkpoint naming any
// other key, or signed by one, verifies as ErrOffCommittee. Such a
// checkpoint is still admitted (it is part of its signer's chain, like one
// awaiting co-signatures) but attests nothing, so it never becomes the
// latest checkpoint, a pin or a finality witness.
//
// Every node of a network must use the same list: it is configuration
// consensus, like the pin. Checkpoints made before the list was set stay
// valid when their committee is a subset of it.

// ErrOffCommittee: a checkpoint names or is signed by a key outside the
// configured committee allowlist.
var ErrOffCommittee = errors.New("tlog: checkpoint committee outside the TELEGRAPH_COMMITTEE allowlist")

var committee struct {
	mu   sync.RWMutex
	keys map[string]bool // nil: no allowlist (any issuer)
}

// SetCommittee installs the allowlist; nil or empty removes it. It clears
// the verification memo, since verdicts on checkpoints depend on it.
func SetCommittee(keys [][]byte) {
	committee.mu.Lock()
	committee.keys = nil
	if len(keys) > 0 {
		committee.keys = make(map[string]bool, len(keys))
		for _, k := range keys {
			committee.keys[string(k)] = true
		}
	}
	committee.mu.Unlock()
	verified.mu.Lock()
	verified.m = map[[32]byte]struct{}{}
	verified.mu.Unlock()
}

// CommitteeSet reports whether an allowlist is configured.
func CommitteeSet() bool {
	committee.mu.RLock()
	defer committee.mu.RUnlock()
	return committee.keys != nil
}

// CommitteeAllows reports whether pub may sit on a committee: always true
// without an allowlist.
func CommitteeAllows(pub []byte) bool {
	committee.mu.RLock()
	defer committee.mu.RUnlock()
	return committee.keys == nil || committee.keys[string(pub)]
}

// Unattested reports whether err only says a checkpoint attests nothing
// (yet): it awaits co-signatures, or its committee is outside the
// allowlist. Stores still admit such checkpoints.
func Unattested(err error) bool {
	return errors.Is(err, ErrCPSigs) || errors.Is(err, ErrOffCommittee)
}
