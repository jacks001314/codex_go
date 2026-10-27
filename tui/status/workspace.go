package status

import (
	"strings"
	"unicode"

	"codex_go/auth"
)

// WorkspaceDisplay mirrors Rust workspace_display (#57366): the trimmed
// backend workspace name, followed by the routing account id in parentheses
// when both are known. A name containing control characters is discarded
// entirely rather than sanitized, and the routing id is used verbatim.
func WorkspaceDisplay(name *string, routing *auth.WorkspaceRouting) string {
	trimmed := ""
	if name != nil {
		candidate := strings.TrimSpace(*name)
		if candidate != "" && strings.IndexFunc(candidate, unicode.IsControl) < 0 {
			trimmed = candidate
		}
	}
	id := ""
	if routing != nil {
		id = routing.ChatGPTAccountID
	}
	switch {
	case trimmed != "" && id != "":
		return trimmed + " (" + id + ")"
	case trimmed != "":
		return trimmed
	default:
		return id
	}
}
