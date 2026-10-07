package tool

import (
	"testing"

	"codex_go/execserver"
)

// Rust #50962 (335c7f8eca, resolve_tool_environment in
// codex-rs/core/src/tools/handlers/mod.rs) decides three things for an
// environment-backed handler: an explicit selector the turn never selected is an
// unknown environment, a selected environment that is not usable is reported
// with the tool own legacy message while the feature is off, and with the
// feature on it gets the shared waiting message.
func TestResolveToolEnvironmentLikeRust(t *testing.T) {
	const legacy = "view_image is unavailable in this session"
	unknownRemote := "unknown turn environment id `remote`"
	unknownOther := "unknown turn environment id `other`"
	local := execserver.LocalEnvironmentID

	ready := &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, ReadyEnvironmentCount: 1}
	readyStable := &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, ReadyEnvironmentCount: 1, StableEnvironmentTools: true}
	starting := &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}}
	startingStable := &UnifiedExecEnvironmentCheck{SelectedEnvironmentIDs: []string{"remote"}, StableEnvironmentTools: true}

	cases := []struct {
		name          string
		check         *UnifiedExecEnvironmentCheck
		environmentID string
		wantID        string
		wantErr       string
	}{
		{"no_readiness_keeps_the_selector", nil, "remote", "remote", ""},
		{"ready_primary_is_usable", ready, "", "", ""},
		{"ready_local_is_usable", ready, local, local, ""},
		{"ready_selected_remote_is_unknown_without_the_feature", ready, "remote", "", unknownRemote},
		{"ready_selected_remote_waits_with_the_feature", readyStable, "remote", "", UnifiedUnavailableEnvironmentMessage},
		{"unready_primary_reports_the_legacy_message", starting, "", "", legacy},
		{"unready_primary_waits_with_the_feature", startingStable, "", "", UnifiedUnavailableEnvironmentMessage},
		{"unready_selected_remote_is_unknown_without_the_feature", starting, "remote", "", unknownRemote},
		{"unready_selected_remote_waits_with_the_feature", startingStable, "remote", "", UnifiedUnavailableEnvironmentMessage},
		{"unselected_remote_is_unknown", ready, "other", "", unknownOther},
		{"unselected_remote_is_unknown_with_the_feature", readyStable, "other", "", unknownOther},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotID, err := ResolveToolEnvironment(testCase.check, testCase.environmentID, legacy)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("ResolveToolEnvironment() error = %v, want nil", err)
				}
			} else if err == nil || err.Error() != testCase.wantErr {
				t.Fatalf("ResolveToolEnvironment() error = %v, want %q", err, testCase.wantErr)
			}
			if gotID != testCase.wantID {
				t.Fatalf("ResolveToolEnvironment() id = %q, want %q", gotID, testCase.wantID)
			}
		})
	}
}
