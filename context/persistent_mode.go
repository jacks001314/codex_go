package context

import (
	_ "embed"
	"strings"
)

// persistentModeDefaultInstructions is Rust core/assets/persistent_mode.md, the
// bundled proactivity and follow-up guidance used when the model catalog
// provides no `persistent_instructions` (Rust
// `PersistentModeState::DEFAULT_INSTRUCTIONS`). The
// {{ approval_request_channel }} placeholder is tailored based on
// send_user_message_async availability.
//
//go:embed templates/persistent_mode.md
var persistentModeDefaultInstructions string

const (
	persistentModeOpenTag  = "<persistent_mode>"
	persistentModeCloseTag = "</persistent_mode>"
	persistentModeKind     = "persistent_mode.instructions"

	// PersistentModeReplacementNotice prefixes a changed instruction set so the
	// model knows the previous persistent-mode guidance is superseded.
	PersistentModeReplacementNotice = "These persistent-mode instructions replace all previously provided persistent-mode instructions."
	// PersistentModeRemovalNotice retires previously delivered guidance when
	// persistent mode is no longer selected.
	PersistentModeRemovalNotice = "The previously provided persistent-mode instructions no longer apply."
)

// PersistentModeInstructionText returns the bounded persistent-mode guidance
// body. An explicit empty catalog value disables the section; a missing value
// uses the bundled default with the approval-request channel tailored to
// send_user_message_async availability.
func PersistentModeInstructionText(catalogInstructions *string, sendUserMessageAsyncAvailable bool) string {
	instructions := persistentModeDefaultInstructions
	if catalogInstructions != nil {
		instructions = *catalogInstructions
	}
	instructions = strings.TrimSpace(instructions)
	channel := ""
	if sendUserMessageAsyncAvailable {
		channel = " via functions.send_user_message_async"
	}
	instructions = strings.ReplaceAll(instructions, "{{ approval_request_channel }}", channel)
	return strings.TrimSpace(instructions)
}

// PersistentModeFragment wraps persistent-mode guidance in the stable developer
// markers Rust's PersistentModeState uses. Empty guidance has no fragment.
func PersistentModeFragment(instructions string) *SimpleFragment {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return nil
	}
	return NewSimpleFragmentWithKind(RoleDeveloper, persistentModeOpenTag, persistentModeCloseTag, "\n"+instructions+"\n", persistentModeKind)
}

// PersistentModeInstructions builds the developer fragment carrying persistent
// mode guidance (Rust PersistentModeState, #41050). Enablement is decided by the
// caller through the centralized features.PersistentModeEnabled check (Rust
// #48611 separates instruction rendering from reasoning-effort selection).
// Returns nil for Guardian sessions.
func PersistentModeInstructions(enabled bool, catalogInstructions *string, sendUserMessageAsyncAvailable bool, guardianSession bool) *SimpleFragment {
	if guardianSession || !enabled {
		return nil
	}
	return PersistentModeFragment(PersistentModeInstructionText(catalogInstructions, sendUserMessageAsyncAvailable))
}
