//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// shortenDenyReadStateRetry keeps recovery tests fast without changing the
// production deadline.
func shortenDenyReadStateRetry(t *testing.T) {
	t.Helper()
	previousDeadline := denyReadACLStateRetryDeadline
	previousInterval := denyReadACLStateRetryInterval
	denyReadACLStateRetryDeadline = 30 * time.Millisecond
	denyReadACLStateRetryInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		denyReadACLStateRetryDeadline = previousDeadline
		denyReadACLStateRetryInterval = previousInterval
	})
}

// TestDenyReadACLStateRecoversMalformedBookkeepingLikeRust mirrors Rust #50940:
// malformed bookkeeping is retried briefly and then rebuilt as an empty state,
// and loading never rewrites the file.
func TestDenyReadACLStateRecoversMalformedBookkeepingLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	path := filepath.Join(t.TempDir(), denyReadACLStateFile)
	malformed := []byte("   ")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	state, err := loadDenyReadACLState(path)
	if err != nil {
		t.Fatalf("loadDenyReadACLState(malformed) error = %v", err)
	}
	if state == nil || len(state.Principals) != 0 {
		t.Fatalf("recovered state = %#v, want empty bookkeeping", state)
	}
	// Recovery rebuilds only on the next successful store; loading preserves the
	// malformed bytes so a failed apply cannot silently drop them.
	if got, err := os.ReadFile(path); err != nil || string(got) != string(malformed) {
		t.Fatalf("malformed state after load = (%q, %v), want it untouched", got, err)
	}
}

// TestDenyReadACLStatePropagatesIOErrorsLikeRust mirrors Rust #50940: a missing
// file is an empty state, while any other I/O failure propagates.
func TestDenyReadACLStatePropagatesIOErrorsLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	missing := filepath.Join(t.TempDir(), denyReadACLStateFile)
	state, err := loadDenyReadACLState(missing)
	if err != nil || state == nil || len(state.Principals) != 0 {
		t.Fatalf("loadDenyReadACLState(missing) = (%#v, %v), want empty state", state, err)
	}
	// A directory in place of the state leaf cannot be read as a file.
	directory := filepath.Join(t.TempDir(), denyReadACLStateFile)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if _, err := loadDenyReadACLState(directory); err == nil {
		t.Fatal("loadDenyReadACLState(directory) succeeded, want an I/O error")
	}
}

// TestDenyReadACLStateRejectsLinkedFilesLikeRust mirrors Rust #50940: a state
// leaf with multiple hard links is rejected before it is read or written, so a
// linked victim's contents are never modified.
func TestDenyReadACLStateRejectsLinkedFilesLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	directory := t.TempDir()
	path := filepath.Join(directory, denyReadACLStateFile)
	victim := filepath.Join(directory, "victim")
	contents := []byte(`{"principals":{"legacy":["C:\\secret"]}}`)
	if err := os.WriteFile(victim, contents, 0o600); err != nil {
		t.Fatalf("WriteFile(victim) error = %v", err)
	}
	if err := os.Link(victim, path); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if _, err := loadDenyReadACLState(path); err == nil || !strings.Contains(err.Error(), "multiple links") {
		t.Fatalf("loadDenyReadACLState(linked) error = %v, want a multiple-links rejection", err)
	}
	state := &persistentDenyReadACLState{Principals: map[string][]string{"sid": {`C:\desired`}}}
	if err := storeDenyReadACLState(path, state); err == nil || !strings.Contains(err.Error(), "multiple links") {
		t.Fatalf("storeDenyReadACLState(linked) error = %v, want a multiple-links rejection", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != string(contents) {
		t.Fatalf("linked victim after rejection = (%q, %v), want it untouched", got, err)
	}
}

// TestDenyReadACLStateStorePreservesFileIdentityLikeRust mirrors Rust #50940:
// writing in place keeps the file's identity and attributes instead of
// replacing it.
func TestDenyReadACLStateStorePreservesFileIdentityLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	path := filepath.Join(t.TempDir(), denyReadACLStateFile)
	first := &persistentDenyReadACLState{Principals: map[string][]string{"sid": {`C:\one`}}}
	if err := storeDenyReadACLState(path, first); err != nil {
		t.Fatalf("storeDenyReadACLState(first) error = %v", err)
	}
	before := denyReadStateFileIdentity(t, path)

	second := &persistentDenyReadACLState{Principals: map[string][]string{"sid": {`C:\one`, `C:\two`}}}
	if err := storeDenyReadACLState(path, second); err != nil {
		t.Fatalf("storeDenyReadACLState(second) error = %v", err)
	}
	after := denyReadStateFileIdentity(t, path)
	if before != after {
		t.Fatalf("state file identity changed: %+v -> %+v", before, after)
	}
	loaded, err := loadDenyReadACLState(path)
	if err != nil {
		t.Fatalf("loadDenyReadACLState() error = %v", err)
	}
	if len(loaded.Principals["sid"]) != 2 {
		t.Fatalf("loaded state = %#v, want the rewritten contents", loaded)
	}
}

// TestDenyReadACLStateWaitsForLegacyWriterLikeRust mirrors Rust #50940: a
// legacy writer that completes its update within the retry window is not
// recovered over, and its state is returned.
func TestDenyReadACLStateWaitsForLegacyWriterLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	path := filepath.Join(t.TempDir(), denyReadACLStateFile)
	valid := []byte(`{"principals":{"legacy":["C:\\existing"]}}`)
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, writeErr := writer.Write(valid)
		writeDone <- writeErr
	}()
	state, err := loadDenyReadACLState(path)
	writeErr := <-writeDone
	_ = writer.Close()
	if writeErr != nil {
		t.Fatalf("legacy writer error = %v", writeErr)
	}
	if err != nil {
		t.Fatalf("loadDenyReadACLState() error = %v", err)
	}
	if len(state.Principals["legacy"]) != 1 {
		t.Fatalf("state = %#v, want the legacy writer's state", state)
	}
}

// TestDenyReadACLStateActiveWriterPreventsRecoveryLikeRust mirrors Rust #50940:
// once the retry window ends, an active writer keeps the state file open and
// the recovery read fails with a sharing violation instead of rebuilding it.
func TestDenyReadACLStateActiveWriterPreventsRecoveryLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	path := filepath.Join(t.TempDir(), denyReadACLStateFile)
	if err := os.WriteFile(path, []byte(`{"principals":{}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer writer.Close()
	if _, err := loadDenyReadACLState(path); !isDenyReadStateSharingViolation(err) {
		t.Fatalf("loadDenyReadACLState(active writer) error = %v, want a sharing violation", err)
	}
}

// TestSyncPersistentDenyReadACLsRebuildsCorruptStateLikeRust mirrors Rust
// #50940: reconciliation rebuilds malformed bookkeeping from the paths it
// successfully applied, keeping the profile's denies intact.
func TestSyncPersistentDenyReadACLsRebuildsCorruptStateLikeRust(t *testing.T) {
	shortenDenyReadStateRetry(t)
	codexHome := t.TempDir()
	statePath := denyReadACLStatePath(codexHome)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(statePath, []byte("   "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	applied, err := SyncPersistentDenyReadACLs(codexHome, []string{secret}, testCapabilitySID)
	if err != nil {
		t.Fatalf("SyncPersistentDenyReadACLs() error = %v", err)
	}
	if len(applied) == 0 || !containsLexicalPath(applied, secret) {
		t.Fatalf("applied = %#v, want %q", applied, secret)
	}
	state, err := loadDenyReadACLState(statePath)
	if err != nil {
		t.Fatalf("loadDenyReadACLState() error = %v", err)
	}
	if !containsLexicalPath(state.Principals[testCapabilitySID], secret) {
		t.Fatalf("rebuilt state = %#v, want only the applied path", state)
	}
}

type denyReadStateIdentity struct {
	volume     uint32
	indexHi    uint32
	indexLo    uint32
	attributes uint32
}

func denyReadStateFileIdentity(t *testing.T, path string) denyReadStateIdentity {
	t.Helper()
	file, err := openDenyReadACLStateForRead(path, false)
	if err != nil {
		t.Fatalf("openDenyReadACLStateForRead() error = %v", err)
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		t.Fatalf("GetFileInformationByHandle() error = %v", err)
	}
	return denyReadStateIdentity{
		volume:     info.VolumeSerialNumber,
		indexHi:    info.FileIndexHigh,
		indexLo:    info.FileIndexLow,
		attributes: info.FileAttributes,
	}
}
