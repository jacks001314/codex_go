package state

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #47630 bounds Guardian's environment id at 256 UTF-8 bytes. Exactly at
// the limit is reviewable; one byte over never reaches inference, while the
// action JSON still renders so manual approval can route it.
func TestGuardianEnvironmentIDLimitLikeRust(t *testing.T) {
	for _, environmentID := range []string{
		strings.Repeat("a", GuardianMaxEnvironmentIDBytes),
		strings.Repeat("\x1f", GuardianMaxEnvironmentIDBytes),
		strings.Repeat("é", GuardianMaxEnvironmentIDBytes/2),
	} {
		action := guardianEnvironmentRequest(environmentID)
		if _, err := BuildPromptWithOptions(action, nil, BuildPromptOptions{}); err != nil {
			t.Fatalf("BuildPromptWithOptions() at the byte limit error = %v", err)
		}

		oversized := environmentID + "a"
		if len(oversized) <= GuardianMaxEnvironmentIDBytes {
			t.Fatalf("fixture %q is not over the byte limit", oversized)
		}
		_, err := BuildPromptWithOptions(guardianEnvironmentRequest(oversized), nil, BuildPromptOptions{})
		if err == nil || err.Error() != "approval environment id exceeds Guardian's 256-byte limit" {
			t.Fatalf("BuildPromptWithOptions() oversized error = %v", err)
		}
		rendered, _, renderErr := FormatGuardianActionPretty(guardianEnvironmentRequest(oversized))
		if renderErr != nil {
			t.Fatalf("FormatGuardianActionPretty() oversized error = %v, want the action renderable for manual approval", renderErr)
		}
		var decoded map[string]any
		if decodeErr := json.Unmarshal([]byte(rendered), &decoded); decodeErr != nil {
			t.Fatalf("rendered action JSON error = %v\n%s", decodeErr, rendered)
		}
		if decoded["environment_id"] != oversized {
			t.Fatalf("rendered environment id = %#v, want the oversized id intact", decoded["environment_id"])
		}
	}
}

func guardianEnvironmentRequest(environmentID string) Action {
	return Action{
		Type:          "request_permissions",
		EnvironmentID: environmentID,
		Permissions:   map[string]any{"network": map[string]any{"enabled": true}},
	}
}
