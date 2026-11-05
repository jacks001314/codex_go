// Package modelfacingid issues token-efficient identifiers for model context.
//
// An ID is a stable salt followed by shuffled decimal digits: every three-digit
// suffix in 000-999 is issued before the six-digit pool opens, then each wider
// pool in turn. Each pool uses a four-round Feistel permutation with BLAKE3 and
// cycle walking (Rust #59540 codex-utils-model-facing-id).
package modelfacingid

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"

	"github.com/zeebo/blake3"
)

const (
	// Version is the snapshot format this package reads and writes.
	Version uint32 = 1
	// groupSize is the number of decimal values added to each wider pool.
	groupSize uint64 = 1_000
	// rounds is part of the snapshot format: changing it changes every ID.
	rounds = 4
)

// ErrInvalidSalt reports an empty salt, which cannot prefix a model-facing ID.
var ErrInvalidSalt = errors.New("a model ID salt must be nonempty")

// UnsupportedVersionError reports a snapshot written by a different format.
type UnsupportedVersionError struct {
	Version uint32
}

// Error implements error.
func (e *UnsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported model-facing ID generator version %d", e.Version)
}

// ID is one generated identifier. The salt and value stay separate so an ID
// that displays like another still round-trips through its snapshot fields.
type ID struct {
	Salt  string `json:"salt"`
	Value uint64 `json:"value"`
}

// String renders the salt followed by the padded value.
func (id ID) String() string {
	return id.Salt + id.Digits()
}

// Digits renders the value zero-padded to a multiple of three digits for
// protocols that supply their own type prefix.
func (id ID) Digits() string {
	return fmt.Sprintf("%0*d", decimalWidth(id.Value), id.Value)
}

// decimalWidth reports the number of digits rounded up to a multiple of three.
func decimalWidth(value uint64) int {
	digits := 1
	for v := value; v >= 10; v /= 10 {
		digits++
	}
	return ((digits + 2) / 3) * 3
}

// State is a resumable snapshot of one generator's position.
type State struct {
	Version     uint32 `json:"version"`
	Seed        uint64 `json:"seed"`
	Salt        string `json:"salt"`
	NextOrdinal uint64 `json:"next_ordinal"`
}

// NewState builds a snapshot from its stored fields; Restore validates it.
func NewState(version uint32, seed uint64, salt string, nextOrdinal uint64) State {
	return State{Version: version, Seed: seed, Salt: salt, NextOrdinal: nextOrdinal}
}

// Generator issues the next ID of one kind.
type Generator struct {
	state State
}

// New starts a sequence for one ID kind from a shared session seed. The salt
// also prefixes each displayed ID; choose a stable single-token salt such as
// "environment".
func New(seed uint64, salt string) (*Generator, error) {
	if salt == "" {
		return nil, ErrInvalidSalt
	}
	return &Generator{state: State{Version: Version, Seed: deriveSeed(seed, salt), Salt: salt}}, nil
}

// Next issues the next ID. After every uint64 value is issued the sequence
// restarts at zero.
func (g *Generator) Next() ID {
	ordinal := g.state.NextOrdinal
	start, end := poolFor(ordinal)
	value := permute(g.state.Seed, start, end, ordinal)
	if ordinal == math.MaxUint64 {
		g.state.NextOrdinal = 0
		slog.Default().Error("model-facing ID generator exhausted all uint64 values and wrapped to zero",
			"salt", g.state.Salt)
	} else {
		g.state.NextOrdinal = ordinal + 1
	}
	return ID{Salt: g.state.Salt, Value: value}
}

// Snapshot captures the position of the next allocation.
func (g *Generator) Snapshot() State {
	return g.state
}

// Restore continues a snapshot's sequence from its next unissued ordinal.
func Restore(state State) (*Generator, error) {
	if state.Version != Version {
		return nil, &UnsupportedVersionError{Version: state.Version}
	}
	if state.Salt == "" {
		return nil, ErrInvalidSalt
	}
	return &Generator{state: state}, nil
}

// deriveSeed salts the session seed with the ID kind's name. The salt prefix in
// the displayed ID and the derived seed must stay in step, so the derivation is
// part of the snapshot format.
func deriveSeed(seed uint64, salt string) uint64 {
	hasher := blake3.New()
	var seedBytes [8]byte
	binary.LittleEndian.PutUint64(seedBytes[:], seed)
	_, _ = hasher.Write(seedBytes[:])
	_, _ = hasher.Write([]byte(salt))
	var derived [8]byte
	// Extended output over a fixed-size buffer always fills the buffer.
	_, _ = io.ReadFull(hasher.Digest(), derived[:])
	return binary.LittleEndian.Uint64(derived[:])
}

// poolFor returns the decimal pool an ordinal belongs to: [0,999], then
// [1000,999999], then each following pool up to the last uint64 value.
func poolFor(ordinal uint64) (uint64, uint64) {
	start, end := uint64(0), groupSize-1
	for ordinal > end {
		start = end + 1
		if next := start * groupSize; next/groupSize == start {
			end = next - 1
		} else {
			end = math.MaxUint64
		}
	}
	return start, end
}

// permute maps an ordinal to a value inside its pool. The smallest balanced
// Feistel domain covering the pool limits cycle walking; the four rounds and
// their input encoding are part of the snapshot format.
func permute(seed, start, end, ordinal uint64) uint64 {
	size := end - start + 1
	bits := 64 - bitsLeadingZeros(size-1)
	halfBits := uint((bits + 1) / 2)
	halfMask := uint64(1)<<halfBits - 1
	inputLen := 17 + int((halfBits+7)/8)
	var input [25]byte
	binary.LittleEndian.PutUint64(input[:8], seed)
	binary.LittleEndian.PutUint64(input[8:16], start/groupSize)

	slot := ordinal - start
	for {
		left, right := slot>>halfBits, slot&halfMask
		for round := uint8(0); round < rounds; round++ {
			input[16] = round
			binary.LittleEndian.PutUint64(input[17:], right)
			sum := blake3.Sum256(input[:inputLen])
			f := binary.LittleEndian.Uint64(sum[:8]) & halfMask
			left, right = right, left^f
		}
		slot = (left << halfBits) | right
		if slot < size {
			return start + slot
		}
	}
}

// bitsLeadingZeros counts the leading zero bits of value, which is nonzero.
func bitsLeadingZeros(value uint64) uint {
	zeros := uint(0)
	for bit := uint64(1) << 63; bit > 0 && value&bit == 0; bit >>= 1 {
		zeros++
	}
	return zeros
}
