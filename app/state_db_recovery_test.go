package app

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/state"
)

func TestLocalStateDBGuidanceMatchesRustText(t *testing.T) {
	startupError := &state.DBRecoveryStartupError{
		DatabasePath: filepath.Join("home", "state_5.sqlite"),
		Detail:       "database is locked",
	}

	var stderr bytes.Buffer
	printLocalStateDBLockedGuidance(&stderr, startupError)
	for _, want := range []string{
		"Codex couldn't start because another Codex process is using its local data.",
		"Quit any other copies of Codex that may still be running, then try again.",
		"Technical details:",
		"  Location: " + startupError.DatabasePath,
		"  Cause: database is locked",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("locked guidance missing %q in:\n%s", want, stderr.String())
		}
	}

	stderr.Reset()
	printLocalStateDBDiagnosticGuidance(&stderr, &state.DBRecoveryStartupError{
		DatabasePath: filepath.Join("home", "logs.sqlite"),
		Detail:       "file is not a database",
	})
	for _, want := range []string{
		"Codex couldn't start because its local database appears to be damaged.",
		"Run `codex doctor` to check your setup and get next-step guidance.",
		"If this keeps happening, share the technical details below when asking for help.",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("diagnostic guidance missing %q in:\n%s", want, stderr.String())
		}
	}
}

func TestLocalStateDBFreshStartConfirmationMatchesRustText(t *testing.T) {
	startupError := &state.DBRecoveryStartupError{
		DatabasePath: filepath.Join("home", "state_5.sqlite"),
		Detail:       "malformed",
	}
	backups := []state.DBRecoveryBackup{{
		OriginalPath: startupError.DatabasePath,
		BackupPath:   filepath.Join("home", "db-backups", "20260706-120000", "state_5.sqlite"),
	}}

	var stderr bytes.Buffer
	if err := confirmLocalStateDBFreshStartRebuild(strings.NewReader(""), &stderr, startupError, backups); err != nil {
		t.Fatalf("confirmLocalStateDBFreshStartRebuild() error = %v", err)
	}
	for _, want := range []string{
		"Codex rebuilt its local database.",
		"Codex detected a damaged local database, moved it into a backup folder, and will continue startup with a fresh database.",
		"Database path: " + startupError.DatabasePath,
		"Backup folder: " + filepath.Dir(backups[0].BackupPath),
		"Continuing startup with a fresh local database...",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("fresh start output missing %q in:\n%s", want, stderr.String())
		}
	}

	var terminalStderr terminalBuffer
	if err := confirmLocalStateDBFreshStartRebuild(newTerminalReader("\n"), &terminalStderr, startupError, nil); err != nil {
		t.Fatalf("terminal confirm error = %v", err)
	}
	if !strings.Contains(terminalStderr.String(), "Backup folder: unavailable") || !strings.Contains(terminalStderr.String(), "Press Enter to continue.") {
		t.Fatalf("terminal fresh start output = %q", terminalStderr.String())
	}
}

func TestLocalStateDBAutoBackupStartMatchesRustText(t *testing.T) {
	startupError := &state.DBRecoveryStartupError{
		DatabasePath: filepath.Join("home", "state_5.sqlite"),
		Detail:       "corrupt",
	}
	var stderr bytes.Buffer
	printLocalStateDBAutoBackupStart(&stderr, startupError)
	for _, want := range []string{
		"Codex couldn't start because its local database appears to be damaged.",
		"Moving the damaged local database aside so Codex can rebuild it from saved data.",
		"Technical details:",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("auto backup start missing %q in:\n%s", want, stderr.String())
		}
	}
}
