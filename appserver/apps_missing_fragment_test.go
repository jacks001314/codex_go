package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

func TestAppsMissingFragmentOverridesStoredCheckpoint(t *testing.T) {
	router, id := pluginWorldStateTestRouter(t)
	t.Cleanup(func() { _ = router.Close() })
	router.services.Apps = apps.NewAppService([]apps.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: true, IsEnabled: true}})
	record, err := router.threadRecord(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	record.Metadata.WorldState, _ = session.EncodeWorldState(&session.WorldState{AppsInstructions: json.RawMessage("true")})
	record.Items = nil
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatal(err)
	}
	item, err := router.appsGuidanceWorldStateInputItem(id, &config.Config{Values: map[string]any{}}, &model.ModelInfo{IncludeAppsUsageInstructions: true})
	if err != nil || item == nil {
		t.Fatalf("missing fragment suppressed by checkpoint: %v %v", item, err)
	}
}

func TestLatestAppsGuidanceSelectsLastContentFragment(t *testing.T) {
	first := "<catalog_apps_instructions>first</catalog_apps_instructions>"
	last := "<catalog_apps_instructions>last</catalog_apps_instructions>"
	retained := latestAppsGuidance([]session.Item{{Role: "developer", Content: []session.ContentPart{
		{Type: "input_text", Text: first}, {Type: "input_text", Text: last},
	}}})
	if retained == nil || *retained != last {
		t.Fatalf("latest=%v", retained)
	}
}
