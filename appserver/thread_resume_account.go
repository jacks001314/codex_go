package appserver

import (
	"strings"

	"codex_go/auth"
	"codex_go/features"
	"codex_go/session"
)

const resumeAccountMismatchMessage = "This thread was created with a different ChatGPT account or workspace. Sign in with the original account to resume it."

// validateResumeAccount mirrors Rust #58890: CLI clients may resume only a
// thread whose recorded ChatGPT user and account match the cached login.
// Untagged threads, other clients, and non-ChatGPT auth retain old behavior.
func (r *RuntimeRouter) validateResumeAccount(record *session.Record, clientName string) error {
	if r == nil || record == nil {
		return nil
	}
	clientName = strings.TrimSpace(clientName)
	if clientName != "codex-tui" && clientName != "codex_exec" {
		return nil
	}
	if strings.TrimSpace(record.Metadata.CreatorUserID) == "" && strings.TrimSpace(record.Metadata.CreatorAccountID) == "" {
		return nil
	}
	cfg, err := r.configForThreadRecord(record, nil)
	if err != nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "cli_resume_account_check") {
		return nil
	}
	snapshot := r.currentCachedAuthSnapshot()
	if snapshot == nil {
		return nil
	}
	if resumeAccountMismatch(record, snapshot) {
		return jsonRPCInvalidRequest(resumeAccountMismatchMessage)
	}
	return nil
}

// resumeAccountMismatch compares only IDs the thread recorded.
func resumeAccountMismatch(record *session.Record, snapshot *auth.AuthDotJSON) bool {
	if record == nil || snapshot == nil {
		return false
	}
	mode := snapshot.Mode()
	if mode != "chatgpt" && mode != "chatgptAuthTokens" {
		return false
	}
	creatorUserID := strings.TrimSpace(record.Metadata.CreatorUserID)
	if creatorUserID != "" && auth.ChatGPTUserIDFromAuth(snapshot) != creatorUserID {
		return true
	}
	creatorAccountID := strings.TrimSpace(record.Metadata.CreatorAccountID)
	if creatorAccountID != "" && auth.AccountIDFromAuthForRestrictions(snapshot) != creatorAccountID {
		return true
	}
	return false
}
