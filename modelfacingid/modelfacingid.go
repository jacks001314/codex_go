// Package modelfacingid issues token-efficient identifiers for model context.
//
// An ID is a stable salt followed by shuffled decimal digits: every three-digit
// suffix in 000-999 is issued before the six-digit pool opens, then each wider
// pool in turn. Each pool uses a four-round Feistel permutation with BLAKE3 and
// cycle walking (Rust #59540 codex-utils-model-facing-id).
package modelfacingid

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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

// ID is one generated identifier. The salt, allocation ordinal and value stay
// separate so an ID that displays like another still round-trips through its
// snapshot fields, and an allocated ID can resume its generator (#60732).
type ID struct {
	Salt    string `json:"salt"`
	Ordinal uint64 `json:"ordinal"`
	Value   uint64 `json:"value"`
}

// String renders the salt followed by the padded value.
func (id ID) String() string {
	return id.Salt + id.Digits()
}

// Digits renders the value zero-padded to a multiple of three digits for
// protocols that supply their own type prefix.
func (id ID) Digits() string {
	return PaddedDigits(id.Value)
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
	return StartingAt(seed, salt, 0)
}

// StartingAt continues a sequence at nextOrdinal using the original seed and
// salt. Rust #60732 ModelFacingIdGenerator::starting_at.
func StartingAt(seed uint64, salt string, nextOrdinal uint64) (*Generator, error) {
	if salt == "" {
		return nil, ErrInvalidSalt
	}
	return &Generator{state: State{
		Version:     Version,
		Seed:        deriveSeed(seed, salt),
		Salt:        salt,
		NextOrdinal: nextOrdinal,
	}}, nil
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
	return ID{Salt: g.state.Salt, Ordinal: ordinal, Value: value}
}

// NextOrdinal reports the position the next allocation will use.
func (g *Generator) NextOrdinal() uint64 {
	return g.state.NextOrdinal
}

// Snapshot captures the position of the next allocation.
func (g *Generator) Snapshot() State {
	return g.state
}

// IDAt recovers the identifier at a stored binding ordinal in this generator's
// sequence (Rust #61339 ModelFacingIdGeneratorState::id_at).
func (s State) IDAt(ordinal uint64) (ID, error) {
	s.NextOrdinal = ordinal
	generator, err := Restore(s)
	if err != nil {
		return ID{}, err
	}
	return generator.Next(), nil
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

// PaddedDigits renders a raw value the way a model-facing ID displays it.
func PaddedDigits(value uint64) string {
	return fmt.Sprintf("%0*d", decimalWidth(value), value)
}

// ModelIDKey identifies one external object within a stable alias namespace.
// Kind is the namespace prefix and Key is the canonical JSON encoding of the
// external identifier (Rust #61339 ModelIdKey).
type ModelIDKey struct {
	Kind string
	Key  string
}

// ModelIDKind is implemented by values that can be aliased in a namespace.
type ModelIDKind interface {
	ModelIDPrefix() string
}

// NewModelIDKey canonicalizes key so equal values always produce the same
// alias lookup. Object keys are sorted recursively, matching Rust's
// sort_all_objects before serialization.
func NewModelIDKey[K ModelIDKind](key K) (ModelIDKey, error) {
	encoded, err := json.Marshal(key)
	if err != nil {
		return ModelIDKey{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return ModelIDKey{}, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return ModelIDKey{}, err
	}
	return ModelIDKey{Kind: key.ModelIDPrefix(), Key: string(canonical)}, nil
}

// ModelIDMap is one namespace's initial generator state and the external keys
// in allocation order (Rust #61339 ModelIdMap).
type ModelIDMap struct {
	Initial State    `json:"initial"`
	Keys    []string `json:"keys"`
}

// NewModelIDMap starts an empty namespace from a session seed.
func NewModelIDMap(seed uint64, kind string) (ModelIDMap, error) {
	generator, err := New(seed, kind)
	if err != nil {
		return ModelIDMap{}, err
	}
	return ModelIDMap{Initial: generator.Snapshot()}, nil
}

// Lookup returns the alias bound to key, if any.
func (m ModelIDMap) Lookup(key string) (*ID, error) {
	for ordinal, bound := range m.Keys {
		if bound != key {
			continue
		}
		id, err := m.Initial.IDAt(uint64(ordinal))
		if err != nil {
			return nil, err
		}
		return &id, nil
	}
	return nil, nil
}

// RegisteredEnvironmentID is an executor ID assigned by the client.
type RegisteredEnvironmentID string

// ModelIDPrefix implements ModelIDKind.
func (RegisteredEnvironmentID) ModelIDPrefix() string { return "env" }

// String returns the registered identifier.
func (id RegisteredEnvironmentID) String() string { return string(id) }

// ModelEnvironmentID is the model-facing `env` alias for a registered
// environment (Rust #61339).
type ModelEnvironmentID uint64

// String renders the alias with its namespace prefix.
func (id ModelEnvironmentID) String() string { return "env" + PaddedDigits(uint64(id)) }

// ModelEnvironmentIDFromID converts a generated alias.
func ModelEnvironmentIDFromID(id ID) ModelEnvironmentID { return ModelEnvironmentID(id.Value) }

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
