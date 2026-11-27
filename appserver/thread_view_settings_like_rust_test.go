package appserver

import "testing"

// Rust #63990: loaded thread metadata reads display settings directly,
// including the selected environment and its runtime workspace roots, without
// rebuilding an execution configuration for the thread.
func TestApplyThreadViewSettingsUsesLiveEnvironmentRootsLikeRust(t *testing.T) {
	thread := &Thread{
		ID:    "thread-live",
		Model: stringPtrIfNotEmpty("gpt-persisted"),
		Environments: []ThreadEnvironment{{
			EnvironmentID:         "persisted",
			CWD:                   "/old",
			RuntimeWorkspaceRoots: []string{"/old"},
		}},
	}
	effort := "high"
	live := []ThreadEnvironment{{
		EnvironmentID:         "remote",
		CWD:                   "/repo",
		RuntimeWorkspaceRoots: []string{"/repo/src", "/repo/docs"},
	}}
	applyThreadViewSettings(thread, &Settings{
		Model:        "gpt-live",
		Effort:       &effort,
		Environments: &live,
	})
	if thread.Model == nil || *thread.Model != "gpt-live" {
		t.Fatalf("model = %#v, want gpt-live", thread.Model)
	}
	if thread.ReasoningEffort == nil || *thread.ReasoningEffort != ReasoningEffort("high") {
		t.Fatalf("reasoning effort = %#v, want high", thread.ReasoningEffort)
	}
	if len(thread.Environments) != 1 || thread.Environments[0].EnvironmentID != "remote" {
		t.Fatalf("environments = %#v, want live remote selection", thread.Environments)
	}
	if got := thread.Environments[0].RuntimeWorkspaceRoots; len(got) != 2 || got[0] != "/repo/src" || got[1] != "/repo/docs" {
		t.Fatalf("runtime workspace roots = %#v", got)
	}
	live[0].RuntimeWorkspaceRoots[0] = "/mutated"
	if thread.Environments[0].RuntimeWorkspaceRoots[0] != "/repo/src" {
		t.Fatal("thread view retained a reference to the settings roots")
	}

	unchanged := &Thread{
		ID:           "thread-omitted",
		Environments: []ThreadEnvironment{{EnvironmentID: "kept"}},
	}
	applyThreadViewSettings(unchanged, &Settings{})
	if len(unchanged.Environments) != 1 || unchanged.Environments[0].EnvironmentID != "kept" {
		t.Fatalf("omitted environments replaced the persisted selection: %#v", unchanged.Environments)
	}
}
