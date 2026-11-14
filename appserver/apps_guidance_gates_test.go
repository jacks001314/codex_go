package appserver

import (
	"strings"
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/model"
)

func TestAppsGuidanceAvailabilityAndCatalogGates(t *testing.T) {
	for _, tc := range []struct {
		name                                                   string
		appsOn, include, modelOn, accessible, enabled, catalog bool
		want                                                   string
	}{
		{"catalog", true, true, true, true, true, true, "catalog literal"},
		{"catalog off", true, true, true, true, true, false, "## Apps (Connectors)"},
		{"apps off", false, true, true, true, true, true, ""},
		{"include off", true, false, true, true, true, true, ""},
		{"model off", true, true, false, true, true, true, ""},
		{"inaccessible", true, true, true, false, true, true, ""},
		{"connector disabled", true, true, true, true, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, id := pluginWorldStateTestRouter(t)
			t.Cleanup(func() { _ = router.Close() })
			router.services.Apps = apps.NewAppService([]apps.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: tc.accessible, IsEnabled: tc.enabled, EnabledExplicit: true}})
			cfg := &config.Config{Values: map[string]any{
				"features":                  map[string]any{"apps": tc.appsOn, "catalog_apps_instructions": tc.catalog},
				"include_apps_instructions": tc.include,
				"apps":                      map[string]any{"drive": map[string]any{"enabled": tc.enabled}},
			}}
			text := "catalog literal"
			info := &model.ModelInfo{IncludeAppsUsageInstructions: tc.modelOn, ModelMessages: &model.ModelMessages{Apps: &model.AppsMessages{UsageInstructions: &text}}}
			item, err := router.appsGuidanceWorldStateInputItem(id, cfg, info)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if item != nil {
					t.Fatalf("disabled guidance=%v", item)
				}
				return
			}
			if item == nil || !strings.Contains(permissionsInputItemText(t, item), tc.want) {
				t.Fatalf("item=%v want %q", item, tc.want)
			}
		})
	}
}
