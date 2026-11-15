package transport

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const Protocol = "TGP1"
const MaxChunk = 2800

// Reassembly bounds. A hostile channel member controls every field of a
// frame, so the Assembler never trusts total beyond MaxFrames, and it
// buffers at most maxPartial unfinished entries and maxBuffered bytes
// (chunks plus slot bookkeeping), evicting the oldest first. MaxFrames
// fits a checkpoint naming ~1.3M chain heads (see docs/STRESS-REPORT.md).
const (
	MaxFrames   = 1 << 15 // ~91 MiB per entry
	maxPartial  = 512
	maxBuffered = 128 << 20
	slotCost    = 24 // bytes per slot header
)

var ErrBadFrame = errors.New("transport: malformed frame")

// Frame: TGP1|<id>|<idx>|<total>|<base64-chunk>. id = entry hash hex,
// so reassembly is self-verifying.
func SplitFrames(id string, data []byte) []string {
	total := (len(data) + MaxChunk - 1) / MaxChunk
	if total == 0 {
		total = 1
	}
	frames := make([]string, total)
	for i := 0; i < total; i++ {
		lo := i * MaxChunk
		hi := lo + MaxChunk
		if hi > len(data) {
			hi = len(data)
		}
		frames[i] = fmt.Sprintf("%s|%s|%d|%d|%s", Protocol, id, i, total,
			base64.StdEncoding.EncodeToString(data[lo:hi]))
	}
	return frames
}

func IsFrame(s string) bool { return strings.HasPrefix(s, Protocol+"|") }

type Assembler struct {
	mu    sync.Mutex
	parts map[string][][]byte
	have  map[string]int
	order []string // partial ids, oldest first
	bytes int
}

func NewAssembler() *Assembler {
	return &Assembler{parts: map[string][][]byte{}, have: map[string]int{}}
}

// drop forgets a partial entry. Caller holds mu.
func (a *Assembler) drop(id string) {
	for _, c := range a.parts[id] {
		a.bytes -= len(c)
	}
	a.bytes -= len(a.parts[id]) * slotCost
	delete(a.parts, id)
	delete(a.have, id)
	for i, o := range a.order {
		if o == id {
			a.order = append(a.order[:i], a.order[i+1:]...)
			break
		}
	}
}

func (a *Assembler) Add(frame string) (data []byte, id string, ok bool, err error) {
	f := strings.SplitN(frame, "|", 5)
	if len(f) != 5 || f[0] != Protocol {
		return nil, "", false, ErrBadFrame
	}
	id = f[1]
	idx, err1 := strconv.Atoi(f[2])
	total, err2 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || idx < 0 || total < 1 || idx >= total || total > MaxFrames ||
		id == "" || len(id) > 128 {
		return nil, "", false, ErrBadFrame
	}
	chunk, err3 := base64.StdEncoding.DecodeString(f[4])
	if err3 != nil {
		return nil, "", false, ErrBadFrame
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.parts[id] == nil {
		if total == 1 {
			return chunk, id, true, nil
		}
		for len(a.order) >= maxPartial || (len(a.order) > 0 && a.bytes+total*slotCost+len(chunk) > maxBuffered) {
			a.drop(a.order[0])
		}
		a.parts[id] = make([][]byte, total)
		a.order = append(a.order, id)
		a.bytes += total * slotCost
	}
	if len(a.parts[id]) != total {
		return nil, id, false, ErrBadFrame // frames for one id disagree on total
	}
	if a.parts[id][idx] == nil {
		a.parts[id][idx] = chunk
		a.have[id]++
		a.bytes += len(chunk)
	}
	if a.have[id] == len(a.parts[id]) {
		out := []byte{}
		for _, c := range a.parts[id] {
			out = append(out, c...)
		}
		a.drop(id)
		return out, id, true, nil
	}
	return nil, id, false, nil
}
