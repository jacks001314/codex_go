package tui

import (
	"reflect"
	"testing"

	"codex_go/appserver"
	"codex_go/config"
)

func TestHookNeedsReviewMatchesRust(t *testing.T) {
	cases := []struct {
		status appserver.HookTrustStatus
		want   bool
	}{
		{appserver.HookTrustUntrusted, true},
		{appserver.HookTrustModified, true},
		{appserver.HookTrustTrusted, false},
		{appserver.HookTrustManaged, false},
	}
	for _, tc := range cases {
		if got := HookNeedsReview(appserver.HookMetadata{TrustStatus: tc.status}); got != tc.want {
			t.Fatalf("HookNeedsReview(%s) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestHooksListEntryForCWDMatchesRust(t *testing.T) {
	response := appserver.HookListResponse{Data: []appserver.HookListEntry{
		{CWD: `/tmp/other`, Warnings: []string{"skip"}},
		{CWD: `/tmp/repo`, Warnings: []string{"review"}},
	}}
	entry := HooksListEntryForCWD(response, `/tmp/repo`)
	if entry.CWD != `/tmp/repo` || !reflect.DeepEqual(entry.Warnings, []string{"review"}) {
		t.Fatalf("HooksListEntryForCWD = %#v", entry)
	}
	missing := HooksListEntryForCWD(response, `/tmp/missing`)
	if missing.CWD != `/tmp/missing` || len(missing.Hooks) != 0 || len(missing.Warnings) != 0 || len(missing.Errors) != 0 {
		t.Fatalf("missing entry = %#v", missing)
	}
}

func TestBuildHookTrustWriteParamsMatchesRust(t *testing.T) {
	params := BuildHookTrustWriteParams([]HookTrustUpdate{
		{Key: `file:C:\repo\.gcode\hooks.json:pre_tool_use:0:0`, CurrentHash: "sha256:a"},
		{Key: "project:post_tool_use:1", CurrentHash: "sha256:b"},
	})
	if !params.ReloadUserConfig || params.FilePath != nil || params.ExpectedVersion != nil {
		t.Fatalf("params metadata = %#v", params)
	}
	if len(params.Edits) != 1 {
		t.Fatalf("edits = %#v", params.Edits)
	}
	edit := params.Edits[0]
	if edit.KeyPath != "hooks.state" || edit.MergeStrategy != config.MergeUpsert {
		t.Fatalf("edit = %#v", edit)
	}
	value := edit.Value.(map[string]any)
	first := value[`file:C:\repo\.gcode\hooks.json:pre_tool_use:0:0`].(map[string]any)
	if first["trusted_hash"] != "sha256:a" {
		t.Fatalf("first trust value = %#v", first)
	}
	second := value["project:post_tool_use:1"].(map[string]any)
	if second["trusted_hash"] != "sha256:b" {
		t.Fatalf("second trust value = %#v", second)
	}
}

func TestStartupHooksReviewNeededAndBypassMatchRust(t *testing.T) {
	entry := startupHooksReviewTestEntry()
	if got := StartupHooksReviewNeededCount(&entry); got != 2 {
		t.Fatalf("review count = %d, want 2", got)
	}
	if !StartupHooksReviewIsNeeded(false, &entry) {
		t.Fatalf("review should be needed without bypass")
	}
	if StartupHooksReviewIsNeeded(true, &entry) {
		t.Fatalf("review should be suppressed with bypass")
	}
	outcome, done := MaybeStartupHooksReviewOutcome(true, entry)
	if !done || outcome.Kind != StartupHooksReviewContinue {
		t.Fatalf("bypass outcome = %#v done=%v", outcome, done)
	}
	if _, done := MaybeStartupHooksReviewOutcome(false, entry); done {
		t.Fatalf("review-needed entry should not auto-continue")
	}
}

func TestStartupHooksTrustUpdatesMatchRust(t *testing.T) {
	entry := startupHooksReviewTestEntry()
	got := StartupHooksTrustUpdates(&entry)
	want := []HookTrustUpdate{
		{Key: "untrusted-hook", CurrentHash: "sha256:untrusted"},
		{Key: "modified-hook", CurrentHash: "sha256:modified"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trust updates = %#v, want %#v", got, want)
	}
	screen := NewStartupHooksReviewScreen(entry)
	if !reflect.DeepEqual(screen.TrustUpdates(), want) {
		t.Fatalf("screen trust updates = %#v", screen.TrustUpdates())
	}
}

func TestStartupHooksReviewRowsUseSelectionColorBar(t *testing.T) {
	screen := NewStartupHooksReviewScreen(startupHooksReviewTestEntry())
	rows := screen.Rows()
	for _, want := range []string{
		"Hooks need review",
		"2 hooks are new or changed.",
		"Hooks can run outside the sandbox after you trust them.",
		"Press enter to confirm or esc to go back",
	} {
		if !containsRow(rows, want) {
			t.Fatalf("rows missing %q: %#v", want, rows)
		}
	}
	if !containsRow(rows, RenderSelectedRow(NumberedSelectionPrefix(0, true)+"Review hooks")) {
		t.Fatalf("rows missing selected review row: %#v", rows)
	}
	screen.HandleKey("down")
	rows = screen.Rows()
	if !containsRow(rows, RenderSelectedRow(NumberedSelectionPrefix(1, true)+"Trust all and continue")) {
		t.Fatalf("rows missing selected trust row: %#v", rows)
	}
}

func TestStartupHooksReviewSingleCountGrammarMatchesRust(t *testing.T) {
	entry := appserver.HookListEntry{CWD: "/repo", Hooks: []appserver.HookMetadata{
		{Key: "one", CurrentHash: "sha256:one", TrustStatus: appserver.HookTrustUntrusted},
	}}
	screen := NewStartupHooksReviewScreen(entry)
	if !containsRow(screen.Rows(), "1 hook is new or changed.") {
		t.Fatalf("rows = %#v", screen.Rows())
	}
}

func TestStartupHooksReviewSelectionOutcomesMatchRust(t *testing.T) {
	screen := NewStartupHooksReviewScreen(startupHooksReviewTestEntry())
	screen.HandleKey("enter")
	if !screen.IsDone() || screen.Outcome().Kind != StartupHooksReviewOpenHooksBrowser || screen.Outcome().Entry == nil {
		t.Fatalf("review outcome = %#v done=%v", screen.Outcome(), screen.IsDone())
	}

	screen = NewStartupHooksReviewScreen(startupHooksReviewTestEntry())
	screen.HandleKey("3")
	if !screen.IsDone() || screen.Outcome().Kind != StartupHooksReviewContinue {
		t.Fatalf("continue outcome = %#v done=%v", screen.Outcome(), screen.IsDone())
	}

	screen = NewStartupHooksReviewScreen(startupHooksReviewTestEntry())
	screen.HandleKey("esc")
	if !screen.IsDone() || screen.Outcome().Kind != StartupHooksReviewContinue {
		t.Fatalf("esc outcome = %#v done=%v", screen.Outcome(), screen.IsDone())
	}
}

func TestStartupHooksReviewTrustAllStatesMatchRust(t *testing.T) {
	screen := NewStartupHooksReviewScreen(startupHooksReviewTestEntry())
	screen.HandleKey("2")
	if screen.IsDone() || !screen.TrustingAll || screen.TrustAllError != "" {
		t.Fatalf("trust-all state done=%v trusting=%v error=%q", screen.IsDone(), screen.TrustingAll, screen.TrustAllError)
	}
	rows := screen.Rows()
	if !containsRow(rows, "Trusting hooks...") || !containsRow(rows, NumberedSelectionPrefix(1, false)+"Trust all and continue (disabled)") {
		t.Fatalf("trusting rows = %#v", rows)
	}
	screen.TrustAllFailed("Failed to trust hooks: config/batchWrite failed in TUI:\nInvalid configuration")
	rows = screen.Rows()
	if screen.TrustingAll || !containsRow(rows, "Failed to trust hooks: config/batchWrite failed in TUI:") || !containsRow(rows, "Invalid configuration") {
		t.Fatalf("trust failed rows = %#v trusting=%v", rows, screen.TrustingAll)
	}
	screen.TrustAllSucceeded()
	if !screen.IsDone() || screen.Outcome().Kind != StartupHooksReviewContinue {
		t.Fatalf("trust success outcome = %#v done=%v", screen.Outcome(), screen.IsDone())
	}
}

func startupHooksReviewTestEntry() appserver.HookListEntry {
	return appserver.HookListEntry{CWD: "/repo", Hooks: []appserver.HookMetadata{
		{Key: "trusted-hook", CurrentHash: "sha256:trusted", TrustStatus: appserver.HookTrustTrusted},
		{Key: "managed-hook", CurrentHash: "sha256:managed", TrustStatus: appserver.HookTrustManaged},
		{Key: "untrusted-hook", CurrentHash: "sha256:untrusted", TrustStatus: appserver.HookTrustUntrusted},
		{Key: "modified-hook", CurrentHash: "sha256:modified", TrustStatus: appserver.HookTrustModified},
	}}
}
