package appserver

import (
	"testing"
	"time"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

func TestAppsGuidanceRuntimeGatesAndResume(t *testing.T) {
	router, id := pluginWorldStateTestRouter(t)
	router.services.Apps = apps.NewAppService([]apps.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: true, IsEnabled: true, EnabledExplicit: true}})
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"apps": true, "catalog_apps_instructions": true}}}
	wording := "  catalog literal {{name}}\n"
	info := &model.ModelInfo{IncludeAppsUsageInstructions: true, ModelMessages: &model.ModelMessages{Apps: &model.AppsMessages{UsageInstructions: &wording}}}
	item, err := router.appsGuidanceWorldStateInputItem(id, cfg, info)
	if err != nil || item == nil {
		t.Fatalf("first=%v %v", item, err)
	}
	record, err := router.threadRecord(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	record.Items = append(record.Items, appsGuidanceSessionItem("turn-1", item, time.Now()))
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatal(err)
	}
	if item, err := router.appsGuidanceWorldStateInputItem(id, cfg, info); err != nil || item != nil {
		t.Fatalf("repeat=%v %v", item, err)
	}
	info.IncludeAppsUsageInstructions = false
	removal, err := router.appsGuidanceWorldStateInputItem(id, cfg, info)
	if err != nil || removal == nil {
		t.Fatalf("model gate removal=%v %v", removal, err)
	}
	record, err = router.threadRecord(session.ThreadID(id), true, true)
	if err != nil {
		t.Fatal(err)
	}
	record.Items = append(record.Items, appsGuidanceSessionItem("turn-2", removal, time.Now()))
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatal(err)
	}
	if item, err := router.appsGuidanceWorldStateInputItem(id, cfg, info); err != nil || item != nil {
		t.Fatalf("removed repeat=%v %v", item, err)
	}
}
