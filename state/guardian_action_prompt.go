package state

import (
	"encoding/json"
	"strings"
)

// marshalGuardianPromptAction renders the exact action JSON the reviewer sees.
func marshalGuardianPromptAction(action Action) (string, error) {
	data, err := json.MarshalIndent(guardianPromptAction(action), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ActionPresentation mirrors Rust guardian_context::ActionPresentation: the
// consumer-specific framing reused by synchronous review sessions.
type ActionPresentation int

const (
	// ActionPresentationSyncFull is the full synchronous reviewer prompt.
	ActionPresentationSyncFull ActionPresentation = iota
	// ActionPresentationSyncDelta frames an incremental synchronous review.
	ActionPresentationSyncDelta
	// ActionPresentationAsync frames the asynchronous action scorer prompt.
	ActionPresentationAsync
)

// plannedActionKind mirrors Rust guardian_context::PlannedActionKind. Command
// covers exec/execve/apply_patch/MCP/request_permissions; terminal input covers
// write_stdin; network covers network access with or without a trigger.
type plannedActionKind int

const (
	plannedActionCommand plannedActionKind = iota
	plannedActionTerminalInput
	plannedActionNetwork
)

// plannedActionKindFor mirrors Rust's kind derivation in
// core/src/guardian/prompt.rs.
func plannedActionKindFor(action Action) (plannedActionKind, bool) {
	switch action.Type {
	case "network_access":
		_, hasTrigger := action.Extra["trigger"]
		return plannedActionNetwork, hasTrigger
	case "write_stdin":
		return plannedActionTerminalInput, false
	default:
		return plannedActionCommand, false
	}
}

// renderPlannedAction mirrors Rust guardian_context::PlannedAction::render: the
// ordered prompt items that frame the exact action JSON for one presentation.
// The caller joins them; the JSON item already carries its trailing newline.
func renderPlannedAction(actionJSON string, kind plannedActionKind, hasTrigger bool, reason string, presentation ActionPresentation) []string {
	if presentation == ActionPresentationAsync {
		return []string{
			"The Codex agent has requested the following action:\n",
			">>> APPROVAL REQUEST START\n",
			"Planned action JSON:\n",
			actionJSON + "\n",
			">>> APPROVAL REQUEST END\n",
		}
	}
	items := make([]string, 0, 8)
	switch kind {
	case plannedActionNetwork:
		items = append(items,
			">>> APPROVAL REQUEST START\n",
			"Below is a proposed network access request under review.\n",
		)
		if hasTrigger {
			items = append(items,
				"The network access was triggered by the action in the `trigger` entry. When assessing this request, focus primarily on whether the triggering command is authorised by the user and whether it is within the rules. The user does not need to have explicitly authorised this exact network connection, as long as the network access is a reasonable consequence of the triggering command.\n\n",
			)
		} else {
			items = append(items,
				"No trigger action was captured for this network access request. When performing the assessment, use the retained transcript and network access JSON to evaluate user authorization and risk.\n\n",
			)
		}
		items = append(items,
			"Assess the exact network access below. Use read-only tool checks when local state matters.\n",
			"Network access JSON:\n",
		)
	default:
		requestedAction := "The Codex agent has requested the following action:\n"
		if presentation == ActionPresentationSyncDelta {
			requestedAction = "The Codex agent has requested the following next action:\n"
		}
		items = append(items, requestedAction, ">>> APPROVAL REQUEST START\n")
		if reason != "" {
			items = append(items, "Retry reason:\n", reason+"\n\n")
		}
		scope := "Assess the exact planned action below. Use read-only tool checks when local state matters.\n"
		if kind == plannedActionTerminalInput {
			scope = "Assess input to the existing terminal, not a fresh command. The `cwd` field is its launch directory; the terminal's current directory and state may have changed. Use the retained transcript and read-only checks when that state matters.\n"
		}
		items = append(items, scope, "Planned action JSON:\n")
	}
	items = append(items, actionJSON+"\n", ">>> APPROVAL REQUEST END\n")
	return items
}

// joinPlannedAction renders the framing the way the review prompt consumes it.
func joinPlannedAction(action Action, presentation ActionPresentation) (string, error) {
	data, err := marshalGuardianPromptAction(action)
	if err != nil {
		return "", err
	}
	kind, hasTrigger := plannedActionKindFor(action)
	return strings.Join(renderPlannedAction(data, kind, hasTrigger, strings.TrimSpace(action.Reason), presentation), ""), nil
}
