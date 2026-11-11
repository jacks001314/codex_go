package modelfacingid

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// Mirrors Rust #59540: every three-digit suffix is issued before the first ID
// drawn from the six-digit pool.
func TestExhaustsThreeDigitIDsBeforeOpeningSixDigitPoolLikeRust(t *testing.T) {
	generator, err := New(42, "environment")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	issued := make(map[string]struct{}, 1000)
	for range 1000 {
		issued[generator.Next().String()] = struct{}{}
	}
	if len(issued) != 1000 {
		t.Fatalf("issued %d distinct IDs, want 1000", len(issued))
	}
	for value := range 1000 {
		want := fmt.Sprintf("environment%03d", value)
		if _, ok := issued[want]; !ok {
			t.Fatalf("three-digit pool missing %q", want)
		}
	}
	if got := generator.Next().String(); got != "environment825109" {
		t.Fatalf("first six-digit ID = %q, want environment825109", got)
	}
}

// Mirrors Rust #60732: the same display text can come from different salts,
// ordinals and values, and all three round-trip through their snapshot fields.
func TestPersistedIDsKeepAllocationFieldsSeparateLikeRust(t *testing.T) {
	cases := []struct {
		salt    string
		ordinal uint64
		value   uint64
	}{
		{salt: "env123", ordinal: 8, value: 0},
		{salt: "env", ordinal: 97, value: 123_000},
	}
	for _, testCase := range cases {
		saved := []byte(fmt.Sprintf(`{"salt":%q,"ordinal":%d,"value":%d}`, testCase.salt, testCase.ordinal, testCase.value))
		var id ID
		if err := json.Unmarshal(saved, &id); err != nil {
			t.Fatalf("unmarshal %s: %v", saved, err)
		}
		if got := id.String(); got != "env123000" {
			t.Fatalf("ID.String() = %q, want env123000", got)
		}
		encoded, err := json.Marshal(id)
		if err != nil {
			t.Fatalf("marshal ID: %v", err)
		}
		if string(encoded) != string(saved) {
			t.Fatalf("ID json = %s, want %s", encoded, saved)
		}
	}
}

// Mirrors Rust #60732: an allocated ID records the ordinal that produced it, so
// a generator can resume without a separate snapshot.
func TestAllocatedIDResumesNextValueLikeRust(t *testing.T) {
	original, err := StartingAt(42, "item", 999)
	if err != nil {
		t.Fatalf("StartingAt() error = %v", err)
	}
	last := original.Next()
	if last.Ordinal != 999 {
		t.Fatalf("last ordinal = %d, want 999", last.Ordinal)
	}
	if got := original.NextOrdinal(); got != 1000 {
		t.Fatalf("NextOrdinal() = %d, want 1000", got)
	}
	saved, err := json.Marshal(last)
	if err != nil {
		t.Fatalf("marshal ID: %v", err)
	}
	var restoredID ID
	if err := json.Unmarshal(saved, &restoredID); err != nil {
		t.Fatalf("unmarshal ID: %v", err)
	}
	resumed, err := StartingAt(42, "item", restoredID.Ordinal+1)
	if err != nil {
		t.Fatalf("StartingAt() error = %v", err)
	}
	if got, want := resumed.Next(), original.Next(); got != want {
		t.Fatalf("resumed ID = %v, want %v", got, want)
	}
}

// Mirrors Rust #59540: a saved cursor resumes the uninterrupted sequence inside
// and between pools.
func TestSerializedSnapshotsResumeTheSequenceLikeRust(t *testing.T) {
	const derivedSeed = uint64(0x9e6e7c742945897e)
	for _, nextOrdinal := range []uint64{437, 1000, 1437} {
		original, err := New(math.MaxUint64, "env2")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		for range nextOrdinal {
			original.Next()
		}
		snapshot := original.Snapshot()
		want := NewState(1, derivedSeed, "env2", nextOrdinal)
		if snapshot != want {
			t.Fatalf("snapshot = %#v, want %#v", snapshot, want)
		}
		saved, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("marshal snapshot: %v", err)
		}
		wantJSON := fmt.Sprintf(`{"version":1,"seed":%d,"salt":"env2","next_ordinal":%d}`, derivedSeed, nextOrdinal)
		if string(saved) != wantJSON {
			t.Fatalf("snapshot json = %s, want %s", saved, wantJSON)
		}
		var restored State
		if err := json.Unmarshal(saved, &restored); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		resumed, err := Restore(restored)
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		for range 1001 {
			if got, wantID := resumed.Next(), original.Next(); got != wantID {
				t.Fatalf("resumed ID = %v, want %v", got, wantID)
			}
		}
	}
}

// Mirrors Rust #59540: version 1 keeps its initial outputs and repeated cycle
// walks stable.
func TestV1KnownOutputsLikeRust(t *testing.T) {
	const seed = uint64(0x123456789abcdef0)
	generator, err := Restore(NewState(1, seed, "environment", 0))
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	for _, want := range []string{"895", "930", "155", "141", "916", "912", "678", "430"} {
		if got := generator.Next().String(); got != "environment"+want {
			t.Fatalf("ID = %q, want environment%s", got, want)
		}
	}
	resumed, err := Restore(NewState(1, seed, "environment", 1605))
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := resumed.Next().String(); got != "environment765577" {
		t.Fatalf("ordinal 1605 ID = %q, want environment765577", got)
	}
}

// Mirrors Rust #59540: the last ID at each width and the first at the next
// width stay stable across restores.
func TestV1KnownOutputsAtEveryPoolBoundaryLikeRust(t *testing.T) {
	const seed = uint64(0x123456789abcdef0)
	cases := []struct {
		boundary uint64
		last     string
		first    string
	}{
		{boundary: 1_000, last: "553", first: "661326"},
		{boundary: 1_000_000, last: "753874", first: "286136338"},
		{boundary: 1_000_000_000, last: "839882170", first: "813648058018"},
		{boundary: 1_000_000_000_000, last: "634462456979", first: "134726697070558"},
		{boundary: 1_000_000_000_000_000, last: "326088534089890", first: "563366069705075074"},
		{boundary: 1_000_000_000_000_000_000, last: "633936232025849076", first: "012827849051678362461"},
	}
	for _, testCase := range cases {
		generator, err := Restore(NewState(1, seed, "env", testCase.boundary-1))
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if got := generator.Next().String(); got != "env"+testCase.last {
			t.Fatalf("boundary %d last ID = %q, want env%s", testCase.boundary, got, testCase.last)
		}
		resumed, err := Restore(generator.Snapshot())
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		next := generator.Next()
		if next.String() != "env"+testCase.first {
			t.Fatalf("boundary %d first ID = %q, want env%s", testCase.boundary, next, testCase.first)
		}
		if got := resumed.Next(); got != next {
			t.Fatalf("resumed boundary ID = %v, want %v", got, next)
		}
	}
}

// Mirrors Rust #59540: one session seed drives several ID kinds, each with its
// own derived seed stored in the snapshot.
func TestSaltsDeriveTheSeedStoredInSnapshotsLikeRust(t *testing.T) {
	cases := []struct {
		seed        uint64
		salt        string
		derivedSeed uint64
	}{
		{seed: 42, salt: "environment", derivedSeed: 0xc9b5d0ac9722ea42},
		{seed: 42, salt: "context-window", derivedSeed: 0x2747db8a6513485b},
		{seed: 43, salt: "environment", derivedSeed: 0x70aaa9fd4bf26e41},
	}
	for _, testCase := range cases {
		generator, err := New(testCase.seed, testCase.salt)
		if err != nil {
			t.Fatalf("New(%d, %q) error = %v", testCase.seed, testCase.salt, err)
		}
		want := NewState(1, testCase.derivedSeed, testCase.salt, 0)
		if got := generator.Snapshot(); got != want {
			t.Fatalf("snapshot for %q = %#v, want %#v", testCase.salt, got, want)
		}
	}
}

// Mirrors Rust #59540: the final ordinal belongs to the last decimal pool and
// its successor restarts the sequence.
func TestWrapsAfterTheLastUint64OrdinalLikeRust(t *testing.T) {
	generator, err := Restore(NewState(1, 17, "env", math.MaxUint64))
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	last := generator.Next()
	if !strings.HasPrefix(last.String(), "env") || last.Value < 1_000_000_000_000_000_000 {
		t.Fatalf("last ordinal ID = %v, want the last decimal pool", last)
	}
	first := NewState(1, 17, "env", 0)
	if got := generator.Snapshot(); got != first {
		t.Fatalf("wrapped snapshot = %#v, want %#v", got, first)
	}
	fresh, err := Restore(first)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	resumed, err := Restore(generator.Snapshot())
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, want := resumed.Next(), fresh.Next(); got != want {
		t.Fatalf("wrapped ID = %v, want %v", got, want)
	}
	if got, want := resumed.Snapshot(), fresh.Snapshot(); got != want {
		t.Fatalf("wrapped snapshot = %#v, want %#v", got, want)
	}
}

// Mirrors Rust #59540: unusable snapshots are rejected instead of resuming.
func TestRejectsUnusableSnapshotsLikeRust(t *testing.T) {
	if _, err := Restore(NewState(2, 0, "env", 0)); err == nil {
		t.Fatal("unsupported version was accepted")
	} else {
		var versionErr *UnsupportedVersionError
		if !errors.As(err, &versionErr) || versionErr.Version != 2 {
			t.Fatalf("Restore() error = %v, want an unsupported version error", err)
		}
	}
	if _, err := Restore(NewState(1, 42, "", 0)); !errors.Is(err, ErrInvalidSalt) {
		t.Fatalf("Restore(empty salt) error = %v, want ErrInvalidSalt", err)
	}
	if _, err := New(42, ""); !errors.Is(err, ErrInvalidSalt) {
		t.Fatalf("New(empty salt) error = %v, want ErrInvalidSalt", err)
	}
}
