package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	LogCommandPreviewLimit = 200
	LogFilePrefix          = "sandbox"
	LogFileSuffix          = "log"
	MaxLogFiles            = 90
)

var (
	setupLogMu   sync.Mutex
	setupLogInit bool
	setupLogFile *os.File
)

// runtimeLog holds the launcher process's pinned sandbox log directory and
// its cached appender. Rust #62439 keeps one rolling appender per launcher
// process instead of reopening the daily log for every note.
var (
	runtimeLogMu   sync.Mutex
	runtimeLogDir  string
	runtimeLogInit bool
	runtimeLogDay  string
	runtimeLogFile *os.File
)

// RetainSetupLog routes every later LogNoteInDir call into file and stops the
// daily rolling-log fallback, mirroring Rust setup_log_writer plus append_line
// (Rust #42342). Provisioning never appends to a caller-controlled log entry.
// The returned func restores the previous state for in-process callers.
func RetainSetupLog(file *os.File) func() {
	setupLogMu.Lock()
	defer setupLogMu.Unlock()
	previousInit := setupLogInit
	previousFile := setupLogFile
	setupLogInit = true
	setupLogFile = file
	return func() {
		setupLogMu.Lock()
		defer setupLogMu.Unlock()
		setupLogInit = previousInit
		setupLogFile = previousFile
	}
}

// ReleaseSetupLog drops the retained handle but keeps the slot initialized so
// late diagnostics cannot reopen a caller-controlled daily log (Rust
// release_setup_log).
func ReleaseSetupLog() {
	setupLogMu.Lock()
	defer setupLogMu.Unlock()
	setupLogInit = true
	setupLogFile = nil
}

// writeSetupLogLine reports handled=false when notes still belong to the daily
// log, and handled=true when the retained setup log consumed or dropped the line.
func writeSetupLogLine(line string) (bool, error) {
	setupLogMu.Lock()
	defer setupLogMu.Unlock()
	if !setupLogInit {
		return false, nil
	}
	if setupLogFile == nil {
		return true, nil
	}
	_, err := setupLogFile.WriteString(line)
	return true, err
}

// InitRuntimeLog pins the sandbox log directory for this launcher process.
// Rust #62439 ignores a relative home so diagnostics never land in the
// caller's working directory.
func InitRuntimeLog(codexHome string) {
	if !filepath.IsAbs(codexHome) {
		return
	}
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	if runtimeLogInit {
		return
	}
	dir := SandboxDir(codexHome)
	_ = os.MkdirAll(dir, 0o700)
	runtimeLogDir = dir
	runtimeLogInit = true
}

// LogRuntimeNote appends a launcher diagnostic through the pinned
// directory's cached appender. It is a no-op before InitRuntimeLog, which
// keeps an unconfigured helper from writing anywhere.
func LogRuntimeNote(note string) error {
	dir := pinnedRuntimeLogDir()
	if dir == "" {
		return nil
	}
	return LogNoteInDir(dir, note)
}

// CloseRuntimeLog releases the cached appender and unpins the runtime log. The
// launcher reaches this only through process exit; tests use it so a pinned
// handle cannot outlive its temporary home.
func CloseRuntimeLog() {
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	if runtimeLogFile != nil {
		_ = runtimeLogFile.Close()
	}
	runtimeLogFile = nil
	runtimeLogDay = ""
	runtimeLogDir = ""
	runtimeLogInit = false
}
func pinnedRuntimeLogDir() string {
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	if !runtimeLogInit {
		return ""
	}
	return runtimeLogDir
}

// writeRuntimeLogLine reuses the pinned appender for the pinned directory and
// reports whether it consumed the line. Once the slot is initialized a failed
// open drops the note instead of falling back to a caller-controlled log.
func writeRuntimeLogLine(baseDir string, line string) bool {
	runtimeLogMu.Lock()
	defer runtimeLogMu.Unlock()
	if !runtimeLogInit || !sameLogDir(runtimeLogDir, baseDir) {
		return false
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	if runtimeLogFile != nil && runtimeLogDay != day {
		_ = runtimeLogFile.Close()
		runtimeLogFile = nil
	}
	if runtimeLogFile == nil {
		file, err := os.OpenFile(LogFilePathForUTCDate(baseDir, now), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return true
		}
		runtimeLogFile = file
		runtimeLogDay = day
	}
	_, _ = runtimeLogFile.WriteString(line)
	return true
}

func sameLogDir(left string, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func CurrentLogFilePath(codexHome string) string {
	return CurrentLogFilePathForCodexHome(codexHome)
}

func CurrentLogFilePathForCodexHome(codexHome string) string {
	return LogFilePathForUTCDate(SandboxDir(codexHome), time.Now().UTC())
}

func LogFilePathForUTCDate(baseDir string, when time.Time) string {
	return filepath.Join(baseDir, fmt.Sprintf("%s.%s.%s", LogFilePrefix, when.UTC().Format("2006-01-02"), LogFileSuffix))
}

func LogNote(codexHome string, note string) error {
	return LogNoteInDir(SandboxDir(codexHome), note)
}

func LogNoteInDir(baseDir string, note string) error {
	line := fmt.Sprintf("[%s %s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), exeLabel(), note)
	if handled, err := writeSetupLogLine(line); handled {
		return err
	}
	if strings.TrimSpace(baseDir) == "" {
		return ErrInvalidRequest
	}
	if writeRuntimeLogLine(baseDir, line) {
		return nil
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(CurrentLogFilePathForBaseDir(baseDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func CurrentLogFilePathForBaseDir(baseDir string) string {
	return LogFilePathForUTCDate(baseDir, time.Now().UTC())
}

func LogStart(command []string, codexHome string) error {
	return LogNote(codexHome, "START: "+CommandPreview(command))
}

func LogSuccess(command []string, codexHome string) error {
	return LogNote(codexHome, "SUCCESS: "+CommandPreview(command))
}

func LogFailure(command []string, detail string, codexHome string) error {
	return LogNote(codexHome, fmt.Sprintf("FAILURE: %s (%s)", CommandPreview(command), detail))
}

func CommandPreview(command []string) string {
	joined := strings.Join(command, " ")
	if len(joined) <= LogCommandPreviewLimit {
		return joined
	}
	return takeBytesAtUTF8Boundary(joined, LogCommandPreviewLimit)
}

func takeBytesAtUTF8Boundary(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func exeLabel() string {
	exe, err := os.Executable()
	if err != nil {
		return "proc"
	}
	label := filepath.Base(exe)
	if label == "." || label == string(filepath.Separator) || strings.TrimSpace(label) == "" {
		return "proc"
	}
	return label
}
