package checkpoint

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// The trust check. A node only syncs or serves once its log has been
// checked against a pinned checkpoint, a hash the operator got out of
// band. Before v0.3.1 the check ran only on an empty store, so following
// the README in order (init, register, then sync) skipped it: the
// node's own register made the store non-empty and it merged whatever the
// channel sent, unchecked.

// Trust is what a sync must do before it may merge gossip.
type Trust int

const (
	TrustOK        Trust = iota // the log holds the pin and audits clean against it
	TrustBootstrap              // no entries from other nodes yet: bootstrap with the pin
	TrustGenesis                // --genesis and the network has no checkpoint yet
	TrustAdopted                // --genesis, a checkpoint appeared and this node is on its committee: pin it
)

var (
	ErrNoPin          = errors.New("no pin: pass --pin <checkpoint-hash> (get it from an operator out of band) to join, or --genesis to start a network that has no checkpoint yet")
	ErrPinNotInLog    = errors.New("the pinned checkpoint is not in the local log, which already holds other nodes' entries: check the pin, or start from a fresh TELEGRAPH_HOME and bootstrap")
	ErrGenesisHasCP   = errors.New("--genesis: this network already has a checkpoint and this node is not on its committee; get the pin from an operator and pass --pin")
	ErrPinNotVerified = errors.New("local log does not verify against the pin")
)

// HasForeign reports whether entries hold anything not signed by pub.
func HasForeign(entries []tlog.Entry, pub []byte) bool {
	for i := range entries {
		if !bytes.Equal(entries[i].PubKey, pub) {
			return true
		}
	}
	return false
}

// CheckTrust decides whether st, owned by pub, may sync. pin is the pin
// to use (the stored one, or a new --pin when none is stored). When the
// result is TrustAdopted, pinOut is the checkpoint to pin.
func CheckTrust(st *store.Store, pub []byte, pin string, genesis bool) (t Trust, pinOut string, err error) {
	if pin == "" {
		if !genesis {
			return 0, "", ErrNoPin
		}
		cp := st.LatestCheckpoint()
		if cp == nil {
			return TrustGenesis, "", nil
		}
		if !InCommittee(cp.Payload.Comm, pub) {
			return 0, "", ErrGenesisHasCP
		}
		if ok, reason := DumpVerdict(st.Entries, cp.HashHex()); !ok {
			return 0, "", fmt.Errorf("%w: %s", ErrPinNotVerified, reason)
		}
		return TrustAdopted, cp.HashHex(), nil
	}
	held := false
	for i := range st.Entries {
		if e := &st.Entries[i]; e.Payload.Kind == tlog.KindCheckpoint && e.HashHex() == pin {
			if err := e.Verify(); err != nil {
				return 0, "", fmt.Errorf("%w: pinned checkpoint: %v", ErrPinNotVerified, err)
			}
			held = true
			break
		}
	}
	if !held {
		if HasForeign(st.Entries, pub) {
			return 0, "", ErrPinNotInLog
		}
		return TrustBootstrap, pin, nil
	}
	if ok, reason := DumpVerdict(st.Entries, pin); !ok {
		return 0, "", fmt.Errorf("%w: %s", ErrPinNotVerified, reason)
	}
	return TrustOK, pin, nil
}
