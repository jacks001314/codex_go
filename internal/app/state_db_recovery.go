package app

import (
	"bufio"
	"fmt"
	"io"

	"codex_go/internal/state"
)

func printLocalStateDBAutoBackupStart(stderr io.Writer, startupError *state.DBRecoveryStartupError) {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "Codex couldn't start because its local database appears to be damaged.")
	fmt.Fprintln(stderr, "Moving the damaged local database aside so Codex can rebuild it from saved data.")
	printLocalStateDBTechnicalDetails(stderr, startupError)
}

func confirmLocalStateDBFreshStartRebuild(stdin io.Reader, stderr io.Writer, startupError *state.DBRecoveryStartupError, backups []state.DBRecoveryBackup) error {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "Codex rebuilt its local database.")
	fmt.Fprintln(stderr, "Codex detected a damaged local database, moved it into a backup folder, and will continue startup with a fresh database.")
	if startupError != nil {
		fmt.Fprintf(stderr, "Database path: %s\n", startupError.DatabasePath)
	}
	if folder := state.DBRecoveryBackupFolder(backups); folder != "" {
		fmt.Fprintf(stderr, "Backup folder: %s\n", folder)
	} else {
		fmt.Fprintln(stderr, "Backup folder: unavailable")
	}
	if isSessionTerminal(stdin) && isSessionTerminal(stderr) {
		fmt.Fprintln(stderr, "Press Enter to continue.")
		_, err := bufio.NewReader(stdin).ReadString('\n')
		if err == io.EOF {
			return nil
		}
		return err
	}
	fmt.Fprintln(stderr, "Continuing startup with a fresh local database...")
	return nil
}

func printLocalStateDBDiagnosticGuidance(stderr io.Writer, startupError *state.DBRecoveryStartupError) {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "Codex couldn't start because its local database appears to be damaged.")
	fmt.Fprintln(stderr, "Run `codex doctor` to check your setup and get next-step guidance.")
	fmt.Fprintln(stderr, "If this keeps happening, share the technical details below when asking for help.")
	printLocalStateDBTechnicalDetails(stderr, startupError)
}

func printLocalStateDBLockedGuidance(stderr io.Writer, startupError *state.DBRecoveryStartupError) {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "Codex couldn't start because another Codex process is using its local data.")
	fmt.Fprintln(stderr, "Quit any other copies of Codex that may still be running, then try again.")
	printLocalStateDBTechnicalDetails(stderr, startupError)
}

func printLocalStateDBTechnicalDetails(stderr io.Writer, startupError *state.DBRecoveryStartupError) {
	fmt.Fprintln(stderr, "Technical details:")
	for _, detail := range state.DBRecoveryTechnicalDetails(startupError) {
		fmt.Fprintf(stderr, "  %s\n", detail)
	}
}
