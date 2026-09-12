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
)

// PersistentModeInstructions builds the developer fragment carrying persistent
// mode guidance (Rust PersistentModeState, #41050). When reasoning effort is
// persistent it uses the catalog instructions when the model provides them
// (an explicit empty string disables the section) and the bundled default
// otherwise, with the approval-request channel tailored to
// send_user_message_async availability; otherwise the fragment is nil. Returns
// nil for Guardian sessions.
func PersistentModeInstructions(reasoningEffort string, catalogInstructions *string, sendUserMessageAsyncAvailable bool, guardianSession bool) *SimpleFragment {
	if guardianSession || !strings.EqualFold(strings.TrimSpace(reasoningEffort), "persistent") {
		return nil
	}
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
	return NewSimpleFragmentWithKind(RoleDeveloper, persistentModeOpenTag, persistentModeCloseTag, "\n"+instructions+"\n", persistentModeKind)
}
