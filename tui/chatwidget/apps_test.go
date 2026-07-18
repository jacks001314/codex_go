package chatwidget

import (
	"strings"
	"testing"

	appsapi "codex_go/apps"
)

func TestAppsCatalogViewUsesRuntimeCatalog(t *testing.T) {
	desc := "Search Drive files."
	installURL := "https://chatgpt.com/apps/google-drive/drive"
	response := appsapi.AppListResponse{Data: []appsapi.AppEntry{
		{
			ID:                 "drive",
			Name:               "Google Drive",
			Description:        &desc,
			InstallURL:         &installURL,
			IsAccessible:       true,
			IsEnabled:          true,
			PluginDisplayNames: []string{"Docs"},
		},
		{
			ID:           "calendar",
			Name:         "Calendar",
			IsAccessible: true,
			IsEnabled:    false,
		},
		{
			ID:           "linear",
			Name:         "Linear",
			InstallURL:   &installURL,
			IsAccessible: false,
		},
	}}

	view := NewAppsCatalogView(response)
	if view.ViewID != AppsSelectionViewID || view.Title != "Apps" || !view.Searchable {
		t.Fatalf("view = %+v", view)
	}
	if len(view.HeaderLines) != 2 || view.HeaderLines[0] != "Use $ to insert an installed app into your prompt." || view.HeaderLines[1] != "Installed 2 of 3 available apps." {
		t.Fatalf("header = %+v", view.HeaderLines)
	}
	if view.SearchPlaceholder != "Type to search apps" {
		t.Fatalf("placeholder = %q", view.SearchPlaceholder)
	}
	if len(view.Items) != 3 {
		t.Fatalf("items = %+v", view.Items)
	}
	first := view.Items[0]
	if first.ID != "drive" || first.Name != "Google Drive" || first.Action != AppsActionOpenLink || !first.DismissOnSelect {
		t.Fatalf("first item = %+v", first)
	}
	for _, want := range []string{"Installed", "Search Drive files.", "Google Drive", "drive"} {
		if !strings.Contains(first.Description+first.SearchValue, want) {
			t.Fatalf("first item missing %q: %+v", want, first)
		}
	}
	if strings.Contains(first.SearchValue, "Docs") {
		t.Fatalf("search value should match Rust label/id only shape: %+v", first)
	}
	if !strings.Contains(view.Items[1].Description, "Installed \u2022 Disabled") {
		t.Fatalf("disabled app description = %q", view.Items[1].Description)
	}
	if !strings.Contains(view.Items[2].Description, "Can be installed") {
		t.Fatalf("installable app description = %q", view.Items[2].Description)
	}
}

func TestAppsCatalogViewPreservesRawConnectorFieldsMatchRust(t *testing.T) {
	desc := "  Search files  "
	installURL := "   "
	response := appsapi.AppListResponse{Data: []appsapi.AppEntry{{
		ID:           " drive ",
		Name:         " Google Drive ",
		Description:  &desc,
		InstallURL:   &installURL,
		IsAccessible: true,
		IsEnabled:    true,
	}}}

	view := NewAppsCatalogView(response)
	if len(view.Items) != 1 {
		t.Fatalf("items = %+v", view.Items)
	}
	item := view.Items[0]
	if item.ID != " drive " || item.Name != " Google Drive " {
		t.Fatalf("raw id/name should be preserved: %+v", item)
	}
	if item.SearchValue != " Google Drive   drive " {
		t.Fatalf("search value should match Rust label/id shape: %q", item.SearchValue)
	}
	if !item.DismissOnSelect || !strings.Contains(item.SelectedDescription, "Press Enter") {
		t.Fatalf("install_url presence should make item selectable: %+v", item)
	}
	if item.Description != "Installed \u2022 Search files" {
		t.Fatalf("description should still trim optional description: %q", item.Description)
	}
}

func TestAppsLoadingAndErrorViews(t *testing.T) {
	loading := AppsLoadingView()
	if loading.Title != "Apps" || loading.Subtitle != "Loading installed and available apps..." || len(loading.Items) != 1 || !loading.Items[0].Disabled {
		t.Fatalf("loading = %+v", loading)
	}
	errored := AppsErrorView("Apps: failed")
	if errored.Title != "Apps" || !strings.Contains(errored.Subtitle, "failed") || !errored.Items[0].DismissOnSelect {
		t.Fatalf("error = %+v", errored)
	}
}
