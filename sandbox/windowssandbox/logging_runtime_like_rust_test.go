package windowssandbox

import (
	"os"
	"strings"
	"testing"
)

func resetRuntimeLogForTest(t *testing.T) {
	t.Helper()
	runtimeLogMu.Lock()
	previous := struct {
		dir  string
		set  bool
		day  string
		file *os.File
	}{runtimeLogDir, runtimeLogInit, runtimeLogDay, runtimeLogFile}
	runtimeLogDir, runtimeLogInit, runtimeLogDay, runtimeLogFile = "", false, "", nil
	runtimeLogMu.Unlock()
	t.Cleanup(func() {
		runtimeLogMu.Lock()
		if runtimeLogFile != nil {
			_ = runtimeLogFile.Close()
		}
		runtimeLogDir, runtimeLogInit, runtimeLogDay, runtimeLogFile = previous.dir, previous.set, previous.day, previous.file
		runtimeLogMu.Unlock()
	})
}

// Rust #62439 pins the launcher log directory once and reuses a single rolling
// appender, so one launcher process does not reopen the daily log per note.
func TestRuntimeLogReusesOneAppenderLikeRust(t *testing.T) {
	home := t.TempDir()
	resetRuntimeLogForTest(t)
	InitRuntimeLog(home)
	if err := LogRuntimeNote("MXC pid=1 phase=prepare_request"); err != nil {
		t.Fatalf("LogRuntimeNote() error = %v", err)
	}
	first := currentRuntimeLogHandle(t)
	if first == nil {
		t.Fatal("the first note did not open the pinned appender")
	}
	if err := LogRuntimeNote("MXC pid=1 phase=spawn prep_ms=3"); err != nil {
		t.Fatalf("LogRuntimeNote() error = %v", err)
	}
	if second := currentRuntimeLogHandle(t); second != first {
		t.Fatal("the second note reopened the log instead of reusing the appender")
	}
	contents := readSandboxLog(t, home)
	for _, want := range []string{"phase=prepare_request", "phase=spawn prep_ms=3"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("sandbox log = %q, want %q", contents, want)
		}
	}
}

// A relative home is ignored so diagnostics never land in the caller's working
// directory, and an unpinned process writes nothing at all.
func TestRuntimeLogRequiresPinnedAbsoluteHomeLikeRust(t *testing.T) {
	workingDir := t.TempDir()
	resetRuntimeLogForTest(t)
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	InitRuntimeLog("relative-home")
	if got := pinnedRuntimeLogDir(); got != "" {
		t.Fatalf("pinnedRuntimeLogDir() = %q, want no pin for a relative home", got)
	}
	if err := LogRuntimeNote("MXC pid=1 phase=prepare_request"); err != nil {
		t.Fatalf("LogRuntimeNote() error = %v", err)
	}
	entries, err := os.ReadDir(workingDir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("an unpinned runtime log wrote %d entries into the working directory", len(entries))
	}
}

// A note for another directory still uses the per-call writer, so unrelated
// callers keep their own log file.
func TestRuntimeLogLeavesOtherDirectoriesAloneLikeRust(t *testing.T) {
	pinned := t.TempDir()
	other := t.TempDir()
	resetRuntimeLogForTest(t)
	InitRuntimeLog(pinned)
	if err := LogRuntimeNote("MXC pid=1 phase=prepare_request"); err != nil {
		t.Fatalf("LogRuntimeNote() error = %v", err)
	}
	if err := LogNoteInDir(other, "unrelated note"); err != nil {
		t.Fatalf("LogNoteInDir() error = %v", err)
	}
	if contents := readLogDir(t, other); !strings.Contains(contents, "unrelated note") {
		t.Fatalf("other log = %q, want the unrelated note", contents)
	}
	if contents := readSandboxLog(t, pinned); strings.Contains(contents, "unrelated note") {
		t.Fatalf("pinned log = %q, want no unrelated note", contents)
	}
}

func currentRuntimeLogHandle(t *testing.T) *os.File {
	t.Helper()
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	return runtimeLogFile
}

func readSandboxLog(t *testing.T, codexHome string) string {
	t.Helper()
	return readLogDir(t, SandboxDir(codexHome))
}

func readLogDir(t *testing.T, baseDir string) string {
	t.Helper()
	data, err := os.ReadFile(CurrentLogFilePathForBaseDir(baseDir))
	if err != nil {
		t.Fatalf("read sandbox log: %v", err)
	}
	return string(data)
}
