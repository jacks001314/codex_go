package tui

import (
	"path/filepath"
	"sort"
	"strings"

	"codex_go/appserver"
)

type ApprovalDecision string

const (
	ApprovalDecisionApprove ApprovalDecision = "approve"
	ApprovalDecisionDeny    ApprovalDecision = "deny"
	ApprovalDecisionAbort   ApprovalDecision = "abort"
)

func ApprovalDecisionFromBool(approved bool) ApprovalDecision {
	if approved {
		return ApprovalDecisionApprove
	}
	return ApprovalDecisionDeny
}

// FileUpdateChangesToDisplay converts app-server file-change payloads into
// the destination display entries used by apply-patch approvals. Each entry
// carries the changed path; moves include both the source and the target in
// the "source -> target" form used by diff rendering (Rust
// file_update_changes_to_display + apply_patch_header.rs, #39285).
func FileUpdateChangesToDisplay(changes []appserver.FileUpdateChange) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if change.Kind.Type == "update" && change.Kind.MovePath != nil {
			movePath := strings.TrimSpace(*change.Kind.MovePath)
			if movePath != "" {
				entry := filepath.ToSlash(path) + " -> " + filepath.ToSlash(movePath)
				if !seen[entry] {
					seen[entry] = true
					out = append(out, entry)
				}
				continue
			}
		}
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
