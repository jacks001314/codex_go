package tui

import "strings"

// Rust parity: codex-rs/tui/src/daybreak.rs. Cyber refusal guidance comes from
// the current model's catalog-advertised access programs.

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

// DaybreakNoticeFromCyberPrograms selects refusal guidance from one model's
// `available_access_programs` metadata (Rust notice_for_model). Missing
// metadata and non-Astra standard access use the application guidance.
func DaybreakNoticeFromCyberPrograms(model string, programs []string) DaybreakNotice {
	hasStandard := false
	hasDaybreak := false
	for _, program := range programs {
		switch strings.ToLower(strings.TrimSpace(program)) {
		case "standard":
			hasStandard = true
		case "daybreak_blue", "daybreak_red":
			hasDaybreak = true
		}
	}
	if hasDaybreak {
		return DaybreakNoticeLimited
	}
	switch strings.TrimSpace(model) {
	case "gpt-6-astra", "gpt-6-astra-wm":
		if hasStandard {
			return DaybreakNoticeAstra
		}
	}
	return DaybreakNoticeApply
}
