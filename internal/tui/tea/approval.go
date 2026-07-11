package tea

import (
	"fmt"
	"strings"

	"codex_go/internal/protocol"
	"codex_go/internal/shell"
)

func approvalRequestFromToolOutput(item *protocol.ThreadItem) (ApprovalRequestMsg, bool) {
	if item == nil || item.Type != "tool_output" || !metadataBool(item.Metadata, "approval_required") {
		return ApprovalRequestMsg{}, false
	}
	bodyParts := []string{}
	if output := strings.TrimSpace(item.Output); output != "" {
		bodyParts = append(bodyParts, output)
	}
	for _, field := range []struct {
		label string
		key   string
	}{
		{label: "Reason", key: "reason"},
		{label: "Justification", key: "justification"},
		{label: "Working directory", key: "cwd"},
		{label: "Sandbox permissions", key: "sandbox_permissions"},
	} {
		if value := metadataString(item.Metadata, field.key); value != "" {
			bodyParts = append(bodyParts, field.label+": "+value)
		}
	}
	return ApprovalRequestMsg{
		ID:      firstNonEmpty(item.ID, "approval"),
		Title:   "Approval required: " + displayValue(item.ToolName, "tool"),
		Body:    strings.Join(bodyParts, "\n"),
		Command: approvalCommandText(item.Metadata),
	}, true
}

func approvalCommandText(metadata map[string]any) string {
	if value := metadataString(metadata, "hook_command"); value != "" {
		return value
	}
	command := metadataStringSlice(metadata, "command")
	if len(command) == 0 {
		return ""
	}
	return shell.StripShellCommandAndEscape(command)
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	value, _ := metadata[key].(bool)
	return value
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func metadataStringSlice(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	switch value := metadata[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
