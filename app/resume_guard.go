package app

import (
	"errors"
	"strings"

	"codex_go/session"
)

// validateResumeTargetSource mirrors Rust #53816
// (tui/src/session_resume.rs::validate_resume_target): interactive CLI resume
// refuses Guardian review threads and, when the thread records a parent,
// points at the session that should be resumed instead.
func validateResumeTargetSource(source string, parentThreadID string) error {
	if !session.IsGuardianReviewSource(source) {
		return nil
	}
	hint := ""
	if parent := strings.TrimSpace(parentThreadID); parent != "" {
		hint = "\nResume its parent session instead: codex resume " + parent
	}
	return errors.New("Cannot resume a Guardian review thread." + hint)
}
