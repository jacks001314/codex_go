package codexapi

import "testing"

// TestApplyMemoryGitChangeObservationLikeRust pins Rust #61939: a
// positive-only probe upgrades an unknown memory workspace status only when
// the observed HEAD matches the workspace HEAD.
func TestApplyMemoryGitChangeObservationLikeRust(t *testing.T) {
	matching := "0123456789abcdef0123456789abcdef01234567"
	other := "89abcdef0123456789abcdef0123456789abcdef"
	known := false
	cases := []struct {
		name           string
		requestKind    ClientRequestKind
		head           *string
		hasChanges     bool
		workspaceKnown *bool
		workspaceHead  string
		wantApplied    bool
	}{
		{name: "matching memory observation", requestKind: ClientRequestMemory, head: &matching, hasChanges: true, workspaceHead: matching, wantApplied: true},
		{name: "turn request", requestKind: ClientRequestTurn, head: &matching, hasChanges: true, workspaceHead: matching},
		{name: "no changes", requestKind: ClientRequestMemory, head: &matching, workspaceHead: matching},
		{name: "no head", requestKind: ClientRequestMemory, hasChanges: true, workspaceHead: matching},
		{name: "different head", requestKind: ClientRequestMemory, head: &other, hasChanges: true, workspaceHead: matching},
		{name: "full scan wins", requestKind: ClientRequestMemory, head: &matching, hasChanges: true, workspaceHead: matching, workspaceKnown: &known},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			metadata := NewClientMetadata("install", "session", "thread", "window")
			metadata.RequestKind = testCase.requestKind
			workspace := ClientWorkspaceMetadata{LatestGitCommitHash: testCase.workspaceHead}
			if testCase.workspaceKnown != nil {
				value := *testCase.workspaceKnown
				workspace.HasChanges = &value
			}
			metadata.Workspaces["/repo"] = workspace
			metadata.ApplyMemoryGitChangeObservation("/repo", testCase.head, testCase.hasChanges)
			updated := metadata.Workspaces["/repo"]
			if testCase.wantApplied {
				if updated.HasChanges == nil || !*updated.HasChanges {
					t.Fatalf("HasChanges = %v, want true", updated.HasChanges)
				}
				return
			}
			if testCase.workspaceKnown != nil {
				if updated.HasChanges == nil || *updated.HasChanges != *testCase.workspaceKnown {
					t.Fatalf("HasChanges = %v, want preserved %v", updated.HasChanges, *testCase.workspaceKnown)
				}
				return
			}
			if updated.HasChanges != nil {
				t.Fatalf("HasChanges = %v, want nil", *updated.HasChanges)
			}
		})
	}
}
