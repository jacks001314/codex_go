package state

import (
	"encoding/json"
	"strings"
)

// marshalGuardianPromptAction renders the exact action JSON the reviewer sees.
func marshalGuardianPromptAction(action Action) (string, error) {
	data, err := json.MarshalIndent(guardianActionJSONValue(action), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// normalizeRequestPermissions mirrors Rust's typed RequestPermissionProfile
// round trip: the reviewed profile renders with the canonical `file_system`
// key even when the caller supplied the legacy camelCase alias.
func normalizeRequestPermissions(permissions map[string]any) map[string]any {
	if len(permissions) == 0 {
		return permissions
	}
	out := make(map[string]any, len(permissions))
	for key, value := range permissions {
		if strings.TrimSpace(key) == "fileSystem" {
			out["file_system"] = value
			continue
		}
		out[key] = value
	}
	return out
}

// guardianActionJSONValue mirrors Rust's guardian_approval_request_to_json plus
// format_guardian_action_pretty: one JSON document per GuardianApprovalRequest
// variant, with snake_case keys and `tool` naming the variant. Go marshals maps
// with sorted keys, matching Rust's `sort_all_objects`, and absent optional
// fields are omitted like Rust's `skip_serializing_if`.
func guardianActionJSONValue(action Action) map[string]any {
	switch strings.TrimSpace(action.Type) {
	case "network_access":
		value := map[string]any{
			"tool":     "network_access",
			"target":   action.Target,
			"host":     action.Host,
			"protocol": action.Protocol,
			"port":     action.Port,
		}
		if trigger, ok := action.Extra["trigger"]; ok && trigger != nil {
			value["trigger"] = trigger
		}
		return value
	case "mcp_tool_call":
		value := map[string]any{
			"tool":      "mcp_tool_call",
			"server":    strings.TrimSpace(action.Server),
			"tool_name": strings.TrimSpace(action.ToolName),
		}
		if action.Arguments != nil {
			value["arguments"] = action.Arguments
		}
		if connectorID := strings.TrimSpace(action.ConnectorID); connectorID != "" {
			value["connector_id"] = connectorID
		}
		if connectorName := strings.TrimSpace(action.ConnectorName); connectorName != "" {
			value["connector_name"] = connectorName
		}
		if toolTitle := strings.TrimSpace(action.ToolTitle); toolTitle != "" {
			value["tool_title"] = toolTitle
		}
		if annotations := actionAnnotationsJSONValue(action.Annotations); annotations != nil {
			value["annotations"] = annotations
		}
		return value
	case "apply_patch":
		return map[string]any{
			"tool":  "apply_patch",
			"cwd":   action.CWD,
			"files": append([]string{}, action.Files...),
			"patch": action.Patch,
		}
	case "request_permissions":
		value := map[string]any{
			"tool":        "request_permissions",
			"turn_id":     action.TurnID,
			"permissions": normalizeRequestPermissions(action.Permissions),
		}
		if reason := strings.TrimSpace(action.Reason); reason != "" {
			value["reason"] = reason
		}
		return value
	case "execve":
		value := map[string]any{
			"tool":    execveToolName(action.Source),
			"program": action.Program,
			"argv":    append([]string{}, action.Argv...),
			"cwd":     action.CWD,
		}
		if permissions := action.AdditionalPermissions; len(permissions) > 0 {
			value["additional_permissions"] = permissions
		}
		return value
	case "write_stdin":
		value := map[string]any{
			"tool":                "write_stdin",
			"environment_id":      action.EnvironmentID,
			"session_id":          action.SessionID,
			"chars":               action.Chars,
			"cwd":                 action.CWD,
			"sandbox_permissions": action.SandboxPermissions,
			"tty":                 action.TTY != nil && *action.TTY,
		}
		if permissions := action.AdditionalPermissions; len(permissions) > 0 {
			value["additional_permissions"] = permissions
		}
		return value
	default:
		value := map[string]any{"tool": "exec_command", "cwd": action.CWD}
		switch {
		case len(action.CommandArgv) > 0:
			value["command"] = append([]string{}, action.CommandArgv...)
		case strings.TrimSpace(action.Command) != "":
			// A command line without its argv still renders as the array Rust's
			// ExecCommand always serializes.
			value["command"] = []string{strings.TrimSpace(action.Command)}
		}
		if permissions := strings.TrimSpace(action.SandboxPermissions); permissions != "" {
			value["sandbox_permissions"] = permissions
		}
		if permissions := action.AdditionalPermissions; len(permissions) > 0 {
			value["additional_permissions"] = permissions
		}
		if justification := strings.TrimSpace(action.Justification); justification != "" {
			value["justification"] = justification
		}
		if action.TTY != nil {
			value["tty"] = *action.TTY
		}
		return value
	}
}

// execveToolName mirrors Rust guardian_command_source_tool_name: an execve
// review is attributed to the launching source.
func execveToolName(source CommandSource) string {
	if source == CommandSourceUnifiedExec {
		return "exec_command"
	}
	return "shell"
}

// actionAnnotationsJSONValue mirrors Rust GuardianMcpAnnotations: only the
// declared hints are rendered, and no hints means no annotations object.
func actionAnnotationsJSONValue(annotations *ActionAnnotations) map[string]any {
	if annotations == nil {
		return nil
	}
	value := map[string]any{}
	if annotations.DestructiveHint != nil {
		value["destructive_hint"] = *annotations.DestructiveHint
	}
	if annotations.OpenWorldHint != nil {
		value["open_world_hint"] = *annotations.OpenWorldHint
	}
	if annotations.ReadOnlyHint != nil {
		value["read_only_hint"] = *annotations.ReadOnlyHint
	}
	if len(value) == 0 {
		return nil
	}
	return value
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
