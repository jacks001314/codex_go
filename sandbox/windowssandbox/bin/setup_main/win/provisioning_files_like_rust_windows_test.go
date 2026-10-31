//go:build windows

package win

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

func TestOpenSetupLogProvisioningModeCreatesFreshLog(t *testing.T) {
	dir := t.TempDir()
	first, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision) error = %v", err)
	}
	first.Close()
	second, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision, second) error = %v", err)
	}
	second.Close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("provisioning logs = %v, %v; want 2 fresh logs", entries, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "sandbox-") || !strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("provisioning log name = %q", entry.Name())
		}
	}
}

func TestOpenSetupLogLegacyModeUsesDailyLog(t *testing.T) {
	dir := t.TempDir()
	log, err := openSetupLog(dir, setupModeFull)
	if err != nil {
		t.Fatalf("openSetupLog(full) error = %v", err)
	}
	log.Close()
	want := windowssandbox.CurrentLogFilePathForBaseDir(dir)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("legacy log %s missing: %v", want, err)
	}
}

func TestPrepareSetupMarkerProvisioningModeRetainsHandleUntilCommit(t *testing.T) {
	home := t.TempDir()
	payload := &setupPayload{CodexHome: home, Mode: setupModeProvision, RealUser: currentAccountName(t)}
	marker, err := prepareSetupMarker(payload)
	if err != nil {
		t.Fatalf("prepareSetupMarker(provision) error = %v", err)
	}
	if marker == nil {
		t.Fatal("provisioning setup must retain the marker handle")
	}
	markerPath := windowssandbox.SetupMarkerPath(home)
	if _, err := os.OpenFile(markerPath, os.O_WRONLY, 0o600); err == nil {
		marker.Close()
		t.Fatal("retained marker must stay exclusively held")
	}
	if err := commitSetupMarker(marker, home, windowssandbox.OfflineUsername, windowssandbox.OnlineUsername, []uint16{8080}, true); err != nil {
		marker.Close()
		t.Fatalf("commitSetupMarker(provision) error = %v", err)
	}
	marker.Close()
	got, err := windowssandbox.ReadSetupMarker(home)
	if err != nil || got == nil {
		t.Fatalf("ReadSetupMarker() = %#v, %v", got, err)
	}
	if got.OfflineUsername != windowssandbox.OfflineUsername || len(got.ProxyPorts) != 1 || got.ProxyPorts[0] != 8080 || !got.AllowLocalBinding {
		t.Fatalf("marker = %#v", got)
	}
}

func TestPrepareSetupMarkerLegacyModeReopensAtCommit(t *testing.T) {
	home := t.TempDir()
	payload := &setupPayload{CodexHome: home, Mode: setupModeFull}
	marker, err := prepareSetupMarker(payload)
	if err != nil {
		t.Fatalf("prepareSetupMarker(full) error = %v", err)
	}
	if marker != nil {
		marker.Close()
		t.Fatal("legacy setup must reopen the marker rather than retain it")
	}
	if err := commitSetupMarker(nil, home, windowssandbox.OfflineUsername, windowssandbox.OnlineUsername, nil, false); err != nil {
		t.Fatalf("commitSetupMarker(full) error = %v", err)
	}
	if _, err := os.Stat(windowssandbox.SetupMarkerPath(home)); err != nil {
		t.Fatalf("legacy marker missing: %v", err)
	}
}

// TestProvisionOnlyNotesUseRetainedSetupLogLikeRust pins Rust setup_log_writer
// plus append_line (#42342): once provisioning retains a fresh log, log_note
// lines land there instead of the daily rolling log.
func TestProvisionOnlyNotesUseRetainedSetupLogLikeRust(t *testing.T) {
	dir := t.TempDir()
	log, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision) error = %v", err)
	}
	restoreSetupLog := windowssandbox.RetainSetupLog(log)
	defer restoreSetupLog()
	if err := windowssandbox.LogNoteInDir(dir, "provisioning note"); err != nil {
		log.Close()
		t.Fatalf("LogNoteInDir() error = %v", err)
	}
	name := filepath.Join(dir, filepath.Base(log.Name()))
	if err := log.Close(); err != nil {
		t.Fatalf("close setup log error = %v", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read setup log error = %v", err)
	}
	if !strings.Contains(string(data), "provisioning note") {
		t.Fatalf("setup log = %q, want the provisioning note", data)
	}
	if daily, err := filepath.Glob(filepath.Join(dir, "sandbox.*.log")); err != nil {
		t.Fatalf("glob daily logs error = %v", err)
	} else if len(daily) != 0 {
		t.Fatalf("daily logs = %v, want none while provisioning retains a log", daily)
	}
}

// TestReleasedSetupLogDropsLateNotesLikeRust pins release_setup_log (#42342):
// after release the slot stays initialized, so late notes are dropped instead of
// reopening a caller-controlled daily log.
func TestReleasedSetupLogDropsLateNotesLikeRust(t *testing.T) {
	dir := t.TempDir()
	log, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision) error = %v", err)
	}
	restoreSetupLog := windowssandbox.RetainSetupLog(log)
	defer restoreSetupLog()
	name := filepath.Join(dir, filepath.Base(log.Name()))
	windowssandbox.ReleaseSetupLog()
	if err := windowssandbox.LogNoteInDir(dir, "late note"); err != nil {
		log.Close()
		t.Fatalf("LogNoteInDir() after release error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close setup log error = %v", err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read setup log error = %v", err)
	}
	if strings.Contains(string(data), "late note") {
		t.Fatalf("setup log = %q, want the late note dropped", data)
	}
	if daily, err := filepath.Glob(filepath.Join(dir, "sandbox.*.log")); err != nil {
		t.Fatalf("glob daily logs error = %v", err)
	} else if len(daily) != 0 {
		t.Fatalf("daily logs = %v, want none after release", daily)
	}
}

// TestTopLevelSetupErrorUsesFreshLogLikeRust pins Rust setup_main main(): an
// unparsed payload defaults to the ProvisionOnly fresh log and never appends to
// an existing caller-controlled daily log (#42342).
func TestTopLevelSetupErrorUsesFreshLogLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := run(nil, io.Discard, io.Discard); err == nil {
		t.Fatal("run(nil) error = nil, want a payload error")
	}
	sbxDir := windowssandbox.SandboxDir(home)
	fresh, err := filepath.Glob(filepath.Join(sbxDir, "sandbox-*.log"))
	if err != nil {
		t.Fatalf("glob fresh logs error = %v", err)
	}
	if len(fresh) != 1 {
		t.Fatalf("fresh logs = %v, want exactly one", fresh)
	}
	data, err := os.ReadFile(fresh[0])
	if err != nil {
		t.Fatalf("read fresh log error = %v", err)
	}
	if !strings.Contains(string(data), "top-level error") {
		t.Fatalf("fresh log = %q, want the top-level error", data)
	}
	if daily, err := filepath.Glob(filepath.Join(sbxDir, "sandbox.*.log")); err != nil {
		t.Fatalf("glob daily logs error = %v", err)
	} else if len(daily) != 0 {
		t.Fatalf("daily logs = %v, want none for an unparsed payload", daily)
	}
}

func currentAccountName(t *testing.T) string {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatalf("LookupAccount() error = %v", err)
	}
	return account
}
