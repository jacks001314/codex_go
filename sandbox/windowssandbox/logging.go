package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	LogCommandPreviewLimit = 200
	LogFilePrefix          = "sandbox"
	LogFileSuffix          = "log"
	MaxLogFiles            = 90
)

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
	if strings.TrimSpace(baseDir) == "" {
		return ErrInvalidRequest
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	line := fmt.Sprintf("[%s %s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), exeLabel(), note)
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
