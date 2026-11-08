package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDesktopChecksReportInstalledAppLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		// The fixture asserts the Windows log directory (%LOCALAPPDATA%\Codex\Logs);
		// Rust only defines that path on Windows.
		t.Skip("windows log layout only")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home+`\AppData\Local`)
	restore := stubDesktopProbes(t)
	defer restore()
	desktopAppProbe = func() (*desktopApp, error) {
		return &desktopApp{identity: "OpenAI.Codex", version: "26.1021.3705.0"}, nil
	}
	desktopLogRootProbe = func(identity string) string { return desktopLogRoot(identity) }
	desktopSecurityProbe = func(*desktopApp) *DoctorCheck {
		return NewCheck("desktop.security.enforcement", "desktop", CheckStatusOK, "no locally visible recent Codex security enforcement was found")
	}
	checks := desktopChecks()
	if len(checks) != 3 {
		t.Fatalf("desktop checks = %d, want 3", len(checks))
	}
	if checks[0].ID != "desktop.app.version" || checks[0].Status != CheckStatusOK ||
		checks[0].Summary != "the desktop application is installed" {
		t.Fatalf("desktop.app.version = %+v", checks[0])
	}
	wantLogDirectory := `log directory: $HOME\AppData\Local\Codex/Logs`
	if runtime.GOOS == "darwin" {
		wantLogDirectory = "log directory: $HOME/Library/Logs/OpenAI.Codex"
	}
	wantDetails := []string{
		"version: 26.1021.3705.0",
		"running: false",
		wantLogDirectory,
	}
	for index, want := range wantDetails {
		if checks[0].Details[index] != want {
			t.Fatalf("detail %d = %q, want %q", index, checks[0].Details[index], want)
		}
	}
	if checks[1].ID != "desktop.app_server.handshake" || checks[1].Summary != "the desktop application is not running" {
		t.Fatalf("handshake = %+v", checks[1])
	}
	if checks[2].ID != "desktop.security.enforcement" {
		t.Fatalf("security = %+v", checks[2])
	}
}

func TestDesktopChecksReportNothingWithoutInstallationLikeRust(t *testing.T) {
	restore := stubDesktopProbes(t)
	defer restore()
	desktopAppProbe = func() (*desktopApp, error) { return nil, nil }
	if checks := desktopChecks(); len(checks) != 0 {
		t.Fatalf("desktop checks = %d, want none (Rust omits the group)", len(checks))
	}
}

func TestDesktopChecksWarnWhenInstallationProbeFailsLikeRust(t *testing.T) {
	restore := stubDesktopProbes(t)
	defer restore()
	desktopAppProbe = func() (*desktopApp, error) { return nil, errors.New("boom") }
	desktopSecurityProbe = func(*desktopApp) *DoctorCheck {
		return NewCheck("desktop.security.enforcement", "desktop", CheckStatusOK, "no locally visible recent Codex security enforcement was found")
	}
	checks := desktopChecks()
	if len(checks) != 2 {
		t.Fatalf("desktop checks = %d, want 2", len(checks))
	}
	if checks[0].Status != CheckStatusWarning ||
		checks[0].Summary != "the desktop application installation could not be inspected" {
		t.Fatalf("desktop.app.version = %+v", checks[0])
	}
	if checks[1].ID != "desktop.security.enforcement" {
		t.Fatalf("security = %+v", checks[1])
	}
}

func TestInspectDesktopSessionSelectsNewestHandshakeLikeRust(t *testing.T) {
	directory := t.TempDir()
	writeDesktopLog(t, directory, "codex-desktop-session-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=success transportKind=stdio",
	})
	writeDesktopLog(t, directory, "codex-desktop-session-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=success transportKind=stdio",
		"2026-01-01T01:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=failure transportKind=stdio",
	})
	running, check := inspectDesktopSession(directory, func(pid int) bool { return pid == 4242 })
	if !running {
		t.Fatal("session should be running")
	}
	if check.Summary != "the desktop app-server failed to initialize" || check.Status != CheckStatusFail {
		t.Fatalf("handshake = %+v", check)
	}
}

func TestInspectDesktopSessionIgnoresUnrecordedAndStoppedSessions(t *testing.T) {
	directory := t.TempDir()
	writeDesktopLog(t, directory, "codex-desktop-session-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=success transportKind=stdio",
	})
	if running, check := inspectDesktopSession(directory, func(int) bool { return false }); running ||
		check.Summary != "the desktop application is not running" {
		t.Fatalf("running = %v, check = %+v", running, check)
	}
	empty := t.TempDir()
	writeDesktopLog(t, empty, "codex-desktop-session-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] starting app-server",
	})
	if running, check := inspectDesktopSession(empty, func(int) bool { return true }); !running ||
		check.Summary != "no desktop app-server handshake was recorded" {
		t.Fatalf("running = %v, check = %+v", running, check)
	}
}

func TestInspectDesktopSessionRequiresLocalWebsocketContext(t *testing.T) {
	directory := t.TempDir()
	writeDesktopLog(t, directory, "codex-desktop-session-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=failure transportKind=websocket",
		"2026-01-01T00:00:01Z  INFO [AppServerConnection] Starting app-server connection hostId=remote transport=websocket",
	})
	if _, check := inspectDesktopSession(directory, func(int) bool { return true }); check.Summary != "no desktop app-server handshake was recorded" {
		t.Fatalf("remote websocket handshake should be ignored: %+v", check)
	}
	writeDesktopLog(t, directory, "codex-desktop-session-4242-t2026-01-01T00-00-01.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] Starting app-server connection hostId=local transport=websocket",
		"2026-01-01T00:00:01Z  INFO [AppServerConnection] initialize_handshake_result outcome=failure transportKind=websocket",
	})
	if _, check := inspectDesktopSession(directory, func(int) bool { return true }); check.Summary != "the desktop app-server failed to initialize" {
		t.Fatalf("local websocket handshake should be reported: %+v", check)
	}
}

func TestParseDesktopLogNameMatchesRustShape(t *testing.T) {
	session, pid, ok := parseDesktopLogName("codex-desktop-abc123-4242-t2026-01-01T00-00-00.log")
	if !ok || session != "abc123" || pid != 4242 {
		t.Fatalf("parseDesktopLogName = %q, %d, %v", session, pid, ok)
	}
	for _, name := range []string{"codex-desktop-abc-t1.log", "desktop-abc-42-tx.log", "codex-desktop-abc-0-tx.log", "codex-desktop-abc-42.log"} {
		if _, _, ok := parseDesktopLogName(name); ok {
			t.Fatalf("parseDesktopLogName(%q) = ok, want rejected", name)
		}
	}
}

func TestRedactedDesktopPathMatchesRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	if got, want := redactedDesktopPath(home+`\AppData\Local\Codex/Logs`), `$HOME\AppData\Local\Codex/Logs`; got != want {
		t.Fatalf("redactedDesktopPath = %q, want %q", got, want)
	}
	if got, want := redactedDesktopPath(`C:\Other\Logs`), "Logs"; got != want {
		t.Fatalf("redactedDesktopPath = %q, want %q", got, want)
	}
}

// TestDesktopChecksFindDarwinSessionLogsOnDisk reproduces the macOS log-path bug
// end to end: the log tree lives at
// ~/Library/Logs/<identity>/<yyyy/mm/dd>/codex-desktop-<session>-<pid>-t<id>.log,
// so joining the day directory and the file name with a literal backslash can
// never find it. It fails on the old `root + "\\" + ...` path and passes once
// both joins use the platform separator.
func TestDesktopChecksFindDarwinSessionLogsOnDisk(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS log layout only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := desktopLogRoot(desktopMacBundleIdentifier)
	directory := filepath.Join(root, time.Now().Format("2006/01/02"))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", directory, err)
	}
	writeDesktopLog(t, directory, "codex-desktop-abc123-4242-t2026-01-01T00-00-00.log", []string{
		"2026-01-01T00:00:00Z  INFO [AppServerConnection] initialize_handshake_result outcome=success transportKind=stdio",
	})

	restore := stubDesktopProbes(t)
	defer restore()
	desktopAppProbe = func() (*desktopApp, error) {
		return &desktopApp{identity: desktopMacBundleIdentifier, version: "26.1021.3705.0"}, nil
	}
	desktopLogRootProbe = func(identity string) string { return desktopLogRoot(identity) }
	desktopRunningProbe = func(_ *desktopApp, pid int) bool { return pid == 4242 }
	desktopSecurityProbe = func(*desktopApp) *DoctorCheck { return nil }

	checks := desktopChecks()
	if len(checks) < 2 {
		t.Fatalf("desktopChecks = %d checks, want the app row plus the handshake row", len(checks))
	}
	if got := checks[0].Details[1]; got != "running: true" {
		t.Fatalf("session log under %s was not discovered: %q", directory, got)
	}
	if checks[1].Summary != "the desktop app-server initialized successfully" {
		t.Fatalf("handshake check = %+v", checks[1])
	}
}

func TestDesktopLogRootMatchesRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALAPPDATA", home+`\AppData\Local`)
	if runtime.GOOS == "darwin" {
		if got, want := desktopLogRoot("OpenAI.Codex"), filepath.Join(home, "Library", "Logs", "OpenAI.Codex"); got != want {
			t.Fatalf("desktopLogRoot = %q, want %q", got, want)
		}
		return
	}
	if runtime.GOOS != "windows" {
		// Rust only defines a log root for macOS and Windows; other platforms have
		// no desktop app to inspect.
		t.Skip("desktop log root is defined per platform")
	}
	if got, want := desktopLogRoot("OpenAI.Codex"), home+`\AppData\Local\Codex/Logs`; got != want {
		t.Fatalf("desktopLogRoot = %q, want %q", got, want)
	}
}

func stubDesktopProbes(t *testing.T) func() {
	t.Helper()
	app, root, running, security := desktopAppProbe, desktopLogRootProbe, desktopRunningProbe, desktopSecurityProbe
	desktopLogRootProbe = func(string) string { return "" }
	desktopRunningProbe = func(*desktopApp, int) bool { return false }
	return func() {
		desktopAppProbe = app
		desktopLogRootProbe = root
		desktopRunningProbe = running
		desktopSecurityProbe = security
	}
}

func writeDesktopLog(t *testing.T, directory string, name string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log %s: %v", name, err)
	}
}
