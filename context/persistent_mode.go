package context

import (
	"strings"
)

// persistentModeDefaultInstructions mirrors Rust core/templates/persistent_mode.md
// (#41050): bundled proactivity and follow-up guidance for persistent reasoning
// effort. The {{ approval_request_channel }} placeholder is tailored based on
// send_user_message_async availability.
const persistentModeDefaultInstructions = `## Proactivity

After you've completed the user task and delivered the final answer, if you are sampled again without a new user request, look for useful follow-ups that directly support the completed work. Favor closing a known open loop, establishing an awaited result, or verifying that a change took effect over inventing unrelated work. Use past user instructions and your knowledge of the user to prioritize follow-ups, not to infer new authorization.

Avoid duplicate user-visible messages within a turn or across turns. For a simple greeting, thanks, or acknowledgment, one brief response or reaction is enough; do not send equivalent text through both functions and final. Keep substantive final answers self-contained, but do not send an extra message that merely repeats an answer, question, blocker, or approval request already communicated.

You may perform safe, non-mutating follow-ups that remain within the user-authorized scope. Persistence does not broaden that scope. For follow-ups or next actions that require new authority, materially expand scope, or make external state changes not already authorized, describe the proposed action{{ approval_request_channel }} and obtain approval before executing it.`

const (
	persistentModeOpenTag  = "<persistent_mode>"
	persistentModeCloseTag = "</persistent_mode>"
	persistentModeKind     = "persistent_mode.instructions"
)

// PersistentModeInstructions builds the developer fragment carrying persistent
// mode guidance (Rust PersistentModeState, #41050). When reasoning effort is
// persistent it uses the catalog instructions (or the bundled default) with the
// approval-request channel tailored to send_user_message_async availability;
// otherwise the fragment is nil. Returns nil for Guardian sessions.
func PersistentModeInstructions(reasoningEffort string, catalogInstructions string, sendUserMessageAsyncAvailable bool, guardianSession bool) *SimpleFragment {
	if guardianSession || !strings.EqualFold(strings.TrimSpace(reasoningEffort), "persistent") {
		return nil
	}
	instructions := strings.TrimSpace(catalogInstructions)
	if instructions == "" {
		instructions = persistentModeDefaultInstructions
	}
	channel := ""
	if sendUserMessageAsyncAvailable {
		channel = " via functions.send_user_message_async"
	}
	instructions = strings.ReplaceAll(instructions, "{{ approval_request_channel }}", channel)
	return NewSimpleFragmentWithKind(RoleDeveloper, persistentModeOpenTag, persistentModeCloseTag, "\n"+instructions+"\n", persistentModeKind)
}
