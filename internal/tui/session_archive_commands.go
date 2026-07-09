package tui

import "strings"

// Rust parity: codex-rs/tui/src/session_archive_commands.rs.

type DeleteConfirmation string

const (
	DeleteConfirmationPrompt DeleteConfirmation = "prompt"
	DeleteConfirmationSkip   DeleteConfirmation = "skip"
)

type SessionArchiveAction string

const (
	SessionArchive   SessionArchiveAction = "archive"
	SessionDelete    SessionArchiveAction = "delete"
	SessionUnarchive SessionArchiveAction = "unarchive"
)

type ResolvedSessionTarget struct {
	SessionID   string
	SessionName string
}

func SessionArchiveSuccessMessage(action SessionArchiveAction, sessionID string, sessionName string) string {
	verb := "Archived"
	switch action {
	case SessionDelete:
		verb = "Deleted"
	case SessionUnarchive:
		verb = "Unarchived"
	}
	sessionID = strings.TrimSpace(sessionID)
	sessionName = strings.TrimSpace(sessionName)
	if sessionName != "" {
		return verb + " session " + sessionName + " (" + sessionID + ")."
	}
	return verb + " session " + sessionID + "."
}

func SessionArchiveCancelledMessage(action SessionArchiveAction) string {
	if action == SessionDelete {
		return "Delete cancelled."
	}
	return "Cancelled."
}

func SessionArchiveSearchScope(action SessionArchiveAction) (string, []bool) {
	switch action {
	case SessionArchive:
		return "active", []bool{false}
	case SessionUnarchive:
		return "archived", []bool{true}
	case SessionDelete:
		return "active or archived", []bool{false, true}
	default:
		return "active", []bool{false}
	}
}

func SessionArchiveNoMatchMessage(action SessionArchiveAction, target string) string {
	scope, _ := SessionArchiveSearchScope(action)
	return "No " + scope + " session found matching '" + strings.TrimSpace(target) + "'."
}

func SessionDeleteNeedsPrompt(action SessionArchiveAction, confirmation DeleteConfirmation) bool {
	return action == SessionDelete && confirmation != DeleteConfirmationSkip
}

func ConfirmSessionDeleteAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

func SessionDeletePromptLines(target ResolvedSessionTarget) []string {
	id := strings.TrimSpace(target.SessionID)
	name := strings.TrimSpace(target.SessionName)
	first := "Permanently delete session " + id + "?"
	if name != "" {
		first = "Permanently delete session '" + name + "' (" + id + ")?"
	}
	return []string{
		first,
		"This cannot be undone. Subagent threads will also be deleted.",
		"Continue? [y/N]: ",
	}
}
