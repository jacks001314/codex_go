package tui

import (
	"encoding/json"
	"strings"
)

// Rust parity: codex-rs/tui/src/daybreak.rs. Read-only Daybreak eligibility used
// to choose the cyber refusal copy. Account-scoped discovery runs in the
// background; pending or failed reads use the neutral Limited copy. Astra takes
// precedence and the TUI never configures the app's access program.

// DaybreakNotice is the account's Daybreak eligibility for refusal copy.
type DaybreakNotice int

const (
	// DaybreakNoticeApply is shown when no access program is active.
	DaybreakNoticeApply DaybreakNotice = iota
	// DaybreakNoticeAstra is shown for Astra models, which Daybreak cannot cover.
	DaybreakNoticeAstra
	// DaybreakNoticeLimited is the neutral default while discovery is pending or
	// failed.
	DaybreakNoticeLimited
)

// DaybreakNoticeForModel maps a model to its refusal-copy eligibility (Rust
// Notice::for_model). Model identifiers match the app's
// isDaybreakUnavailableModel.
func DaybreakNoticeForModel(notice DaybreakNotice, model string) DaybreakNotice {
	switch strings.TrimSpace(model) {
	case "gpt-6-astra", "gpt-6-astra-wm":
		return DaybreakNoticeAstra
	case "gpt-5.6-sol":
		return notice
	default:
		return DaybreakNoticeLimited
	}
}

// DaybreakVerifiedAccess is the `/accounts/verified_access` response.
type DaybreakVerifiedAccess struct {
	Programs []DaybreakProgram `json:"programs"`
}

// DaybreakProgram is one access program entry. Unknown programs are ignored.
type DaybreakProgram struct {
	Program string            `json:"program"`
	State   string            `json:"state,omitempty"`
	Grants  []json.RawMessage `json:"grants,omitempty"`
}

// Notice maps the account's programs to the refusal-copy eligibility (Rust
// VerifiedAccess::notice): the apply copy is used only when no cyber access is
// active and no grants exist.
func (a DaybreakVerifiedAccess) Notice() DaybreakNotice {
	for _, program := range a.Programs {
		if program.Program == "cyber" && (program.State != "inactive" || len(program.Grants) > 0) {
			return DaybreakNoticeLimited
		}
	}
	return DaybreakNoticeApply
}
