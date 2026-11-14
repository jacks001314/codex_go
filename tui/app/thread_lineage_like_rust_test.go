package app

import (
	"codex_go/appserver"
	"testing"
)

func TestReconstructedSessionUsesTargetLineageLikeRust(t *testing.T) {
	oldParent := "00000000-0000-0000-0000-000000000001"
	otherParent := "00000000-0000-0000-0000-000000000002"
	invalid := "not-a-thread"
	title := "Previous parent"
	for _, tc := range []struct {
		name       string
		parent     *string
		wantParent string
		keepTitle  bool
	}{
		{"changed", &otherParent, otherParent, false},
		{"absent", nil, "", false},
		{"invalid", &invalid, "", false},
		{"same", &oldParent, oldParent, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := ThreadSessionState{ThreadID: "primary", ForkedFromID: &oldParent, ForkParentTitle: &title, PermissionProfile: "stale"}
			read := SessionStateForThreadRead(&primary, ThreadSessionDefaults{PermissionProfile: "active"}, ThreadReadSnapshot{ThreadID: "target", ForkedFromID: tc.parent})
			if read.PermissionProfile != "active" {
				t.Fatal("active permissions lost")
			}
			edited := threadSessionStateFromPromptEditResponse(&appserver.Thread{ID: "target", ForkedFromID: tc.parent}, primary)
			for name, state := range map[string]ThreadSessionState{"read": read, "edit": edited} {
				parent := ""
				if state.ForkedFromID != nil {
					parent = *state.ForkedFromID
				}
				if parent != tc.wantParent {
					t.Errorf("%s parent=%q want=%q", name, parent, tc.wantParent)
				}
				if tc.keepTitle {
					if state.ForkParentTitle == nil || *state.ForkParentTitle != title {
						t.Errorf("%s lost unchanged parent's title", name)
					}
				} else if state.ForkParentTitle != nil {
					t.Errorf("%s retained stale title", name)
				}
				if state.ForkedFromID != nil && (state.ForkedFromID == tc.parent || state.ForkedFromID == primary.ForkedFromID) {
					t.Errorf("%s aliases input parent", name)
				}
			}
			if *primary.ForkedFromID != oldParent || *primary.ForkParentTitle != title {
				t.Fatal("source mutated")
			}
		})
	}
}
