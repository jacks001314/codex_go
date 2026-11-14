package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/model"
	"codex_go/session"
)

func TestLatestAppsGuidanceRawHistoryAndRollback(t *testing.T) {
	first := "<catalog_apps_instructions>first</catalog_apps_instructions>"
	last := "<catalog_apps_instructions>last</catalog_apps_instructions>"
	raw, _ := json.Marshal(modelInputTextMessage("developer", last))
	spoof, _ := json.Marshal(modelInputTextMessage("user", "<catalog_apps_instructions>user text</catalog_apps_instructions>"))
	items := []session.Item{
		{Role: "developer", Text: first},
		{Raw: raw},
		{Raw: spoof},
	}
	got := latestAppsGuidance(items)
	if got == nil || *got != last {
		t.Fatalf("raw latest=%v", got)
	}
	got = latestAppsGuidance(items[:1])
	if got == nil || *got != first {
		t.Fatalf("rollback latest=%v", got)
	}
	if got := latestAppsGuidance(nil); got != nil {
		t.Fatal("empty history has retained guidance")
	}
}

func TestLegacyCatalogDisabledReenablesBundledApps(t *testing.T) {
	bundled := model.ResolveAppsInstructions(nil, "codex_apps")
	retained := "<apps_instructions>" + bundled.UsageInstructions + "</apps_instructions>"
	_, text := appsInstructionsTransition(json.RawMessage(`{"instructions":null,"catalog":false}`), &retained, true, bundled)
	if text == "" {
		t.Fatal("legacy disabled catalog checkpoint suppressed bundled reenable")
	}
}
