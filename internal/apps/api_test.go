package apps

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestListSortsAndClonesApps(t *testing.T) {
	service := NewAppService([]AppEntry{
		{ID: "b", Name: "Beta", Labels: []string{"two"}},
		{ID: "a", Name: "Alpha", Labels: []string{"one"}},
	})
	response, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Apps) != 2 || response.Apps[0].ID != "a" {
		t.Fatalf("unexpected apps: %#v", response.Apps)
	}
	response.Apps[0].Labels[0] = "mutated"
	next, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List(next) error = %v", err)
	}
	if next.Apps[0].Labels[0] != "one" {
		t.Fatalf("service leaked label slice: %#v", next.Apps[0])
	}
}

func TestAddReplacesExistingApp(t *testing.T) {
	service := NewAppService(nil)
	service.Add(&AppEntry{ID: "app", Name: "Old"})
	service.Add(&AppEntry{ID: "app", Name: "New", Enabled: true})
	response, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Apps) != 1 || response.Apps[0].Name != "New" || !response.Apps[0].Enabled {
		t.Fatalf("unexpected app list: %#v", response.Apps)
	}
}

func TestListRejectsInvalidCursor(t *testing.T) {
	service := NewAppService([]AppEntry{{ID: "app"}})
	cursor := "bad"
	if _, err := service.List(&AppListParams{Cursor: &cursor}); !errors.Is(err, ErrInvalidAppRequest) {
		t.Fatalf("List() error = %v, want ErrInvalidAppRequest", err)
	}
	blankCursor := "  "
	if _, err := service.List(&AppListParams{Cursor: &blankCursor}); !errors.Is(err, ErrInvalidAppRequest) {
		t.Fatalf("List(blank cursor) error = %v, want ErrInvalidAppRequest", err)
	}
}

func TestListResponseJSONMatchesRustSchema(t *testing.T) {
	service := NewAppService([]AppEntry{{
		ID:        "app",
		Name:      "App",
		LightIcon: &IconAsset{URL: "https://example.test/light.png"},
		DarkIcon:  &IconAsset{URL: "https://example.test/dark.png"},
		Enabled:   true,
	}})
	response, err := service.List(nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(data)
	if strings.Contains(text, `"apps"`) {
		t.Fatalf("JSON contains legacy apps field: %s", text)
	}
	if !strings.Contains(text, `"data"`) || !strings.Contains(text, `"nextCursor":null`) {
		t.Fatalf("JSON = %s", text)
	}
	for _, legacy := range []string{`"enabled"`, `"lightIcon"`, `"darkIcon"`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("JSON contains legacy AppInfo field %s: %s", legacy, text)
		}
	}
	if !strings.Contains(text, `"pluginDisplayNames":[]`) {
		t.Fatalf("JSON missing required pluginDisplayNames array: %s", text)
	}
}

func TestAppEntryMarshalNormalizesNestedRustShape(t *testing.T) {
	entry := &AppEntry{
		ID:   "drive",
		Name: "Drive",
		Branding: map[string]any{
			"iconUrl":             "https://example.test/icon.png",
			"color":               "#4285f4",
			"website":             "https://drive.example.test",
			"docsUrl":             "https://docs.example.test/drive",
			"isDiscoverableApp":   true,
			"privacyPolicy":       "https://drive.example.test/privacy",
			"unexpectedExtension": "drop-me",
		},
		AppMetadata: map[string]any{
			"categories":        []any{"productivity"},
			"sub_categories":    []any{"docs"},
			"seo_description":   "Find Drive files",
			"screenshots":       []any{map[string]any{"url": "https://example.test/shot.png", "fileId": "file-1", "userPrompt": "open"}},
			"version_id":        "v1",
			"first_party_type":  "connector",
			"unknown_extension": "drop-me",
		},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal app entry error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal app entry error = %v", err)
	}
	branding := payload["branding"].(map[string]any)
	if branding["website"] != "https://drive.example.test" || branding["privacyPolicy"] != "https://drive.example.test/privacy" || branding["isDiscoverableApp"] != true {
		t.Fatalf("branding = %#v", branding)
	}
	for _, legacy := range []string{"iconUrl", "color", "docsUrl", "unexpectedExtension"} {
		if _, ok := branding[legacy]; ok {
			t.Fatalf("branding leaked %q: %#v", legacy, branding)
		}
	}
	metadata := payload["appMetadata"].(map[string]any)
	if metadata["seoDescription"] != "Find Drive files" || metadata["versionId"] != "v1" || metadata["firstPartyType"] != "connector" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, ok := metadata["unknown_extension"]; ok {
		t.Fatalf("metadata leaked extension: %#v", metadata)
	}
	if _, ok := metadata["review"]; !ok || metadata["review"] != nil {
		t.Fatalf("metadata review should be required null: %#v", metadata)
	}
}

func TestMergeConnectorsAndPluginPlaceholders(t *testing.T) {
	description := "Calendar from plugin"
	installURL := "https://example.com/install/calendar"
	logoURL := "https://example.com/logo.png"
	directory := MergePluginConnectors([]AppEntry{{ID: "alpha", Name: "Alpha"}}, []PluginConnector{
		{ID: "calendar", Name: "Google Calendar", Description: &description, InstallURL: &installURL, LogoURL: &logoURL, PluginDisplayName: "Plugin A"},
		{ID: "calendar", PluginDisplayName: "Plugin B"},
	})
	if len(directory) != 2 {
		t.Fatalf("directory = %#v, want two apps", directory)
	}
	pluginApp := appByIDForTest(directory, "calendar")
	if pluginApp == nil || pluginApp.Name != "Google Calendar" || pluginApp.Description == nil || *pluginApp.Description != "Calendar from plugin" {
		t.Fatalf("plugin placeholder = %#v", pluginApp)
	}
	if pluginApp.InstallURL == nil || *pluginApp.InstallURL != installURL || pluginApp.LogoURL == nil || *pluginApp.LogoURL != logoURL {
		t.Fatalf("plugin placeholder assets = %#v", pluginApp)
	}
	if got := pluginApp.PluginDisplayNames; len(got) != 2 || got[0] != "Plugin A" || got[1] != "Plugin B" {
		t.Fatalf("plugin placeholder names = %#v", got)
	}
	accessible := MergeConnectors(directory, []AppEntry{{
		ID:                 "calendar",
		Name:               "Google Calendar",
		IsAccessible:       true,
		PluginDisplayNames: []string{"Plugin B", "Plugin A", "Plugin B"},
	}})
	if len(accessible) != 2 || accessible[0].ID != "calendar" || !accessible[0].IsAccessible {
		t.Fatalf("merged apps = %#v", accessible)
	}
	if accessible[0].Name != "Google Calendar" {
		t.Fatalf("merged name = %q", accessible[0].Name)
	}
	if accessible[0].InstallURL == nil || *accessible[0].InstallURL != "https://chatgpt.com/apps/calendar/calendar" {
		t.Fatalf("install url = %+v", accessible[0].InstallURL)
	}
	if got := accessible[0].PluginDisplayNames; len(got) != 2 || got[0] != "Plugin A" || got[1] != "Plugin B" {
		t.Fatalf("plugin display names = %#v", got)
	}
	accessibleInstallURL := "https://chatgpt.com/apps/google-calendar/calendar"
	accessibleWithURL := MergeConnectors(directory, []AppEntry{{
		ID:           "calendar",
		Name:         "Google Calendar",
		IsAccessible: true,
		InstallURL:   &accessibleInstallURL,
	}})
	if accessibleWithURL[0].InstallURL == nil || *accessibleWithURL[0].InstallURL != accessibleInstallURL {
		t.Fatalf("accessible install url = %+v", accessibleWithURL[0].InstallURL)
	}
	disabledAccessible := MergeConnectors(directory, []AppEntry{{
		ID:              "calendar",
		Name:            "Google Calendar",
		IsAccessible:    true,
		IsEnabled:       false,
		EnabledExplicit: true,
	}})
	if disabledAccessible[0].IsEnabled {
		t.Fatalf("disabled accessible connector was re-enabled: %#v", disabledAccessible[0])
	}
}

func appByIDForTest(apps []AppEntry, id string) *AppEntry {
	for i := range apps {
		if apps[i].ID == id {
			return &apps[i]
		}
	}
	return nil
}

func TestAppsConfigEnabledState(t *testing.T) {
	config := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{
			"_default": map[string]any{"enabled": false},
			"drive":    map[string]any{"enabled": true},
			"calendar": map[string]any{"enabled": false},
		},
	})
	out := WithAppEnabledState([]AppEntry{
		{ID: "calendar", Name: "Calendar", IsEnabled: true},
		{ID: "drive", Name: "Drive", IsEnabled: true},
		{ID: "mail", Name: "Mail", IsEnabled: true},
	}, config, nil)
	enabled := map[string]bool{}
	for _, app := range out {
		enabled[app.ID] = app.IsEnabled
	}
	if enabled["calendar"] || !enabled["drive"] || enabled["mail"] {
		t.Fatalf("enabled state = %#v", enabled)
	}
	noDefault := AppsConfigFromValues(map[string]any{
		"apps": map[string]any{
			"drive": map[string]any{"enabled": true},
		},
	})
	preserved := WithAppEnabledState([]AppEntry{{ID: "calendar", Name: "Calendar", IsEnabled: false, EnabledExplicit: true}}, noDefault, nil)
	if len(preserved) != 1 || preserved[0].IsEnabled {
		t.Fatalf("enabled fallback should preserve disabled connector: %#v", preserved)
	}
}

func TestListMergesProvidersPluginConnectorsAndCache(t *testing.T) {
	directory := &fakeDirectoryProvider{apps: []AppEntry{{ID: "drive", Name: "Drive"}}}
	accessible := &fakeAccessibleProvider{apps: []AppEntry{{
		ID:                 "drive",
		Name:               "Google Drive",
		IsAccessible:       true,
		PluginDisplayNames: []string{"Docs Plugin"},
	}}}
	service := NewAppService(nil)
	service.SetDirectoryProvider(directory)
	service.SetAccessibleProvider(accessible)
	service.SetPluginConnectors([]PluginConnector{{ID: "calendar"}})
	service.SetConfigValues(map[string]any{
		"apps": map[string]any{"calendar": map[string]any{"enabled": false}},
	})
	response, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Apps) != 2 {
		t.Fatalf("apps = %#v, want drive and calendar", response.Apps)
	}
	if response.Apps[0].ID != "drive" || !response.Apps[0].IsAccessible || response.Apps[0].Name != "Google Drive" {
		t.Fatalf("drive app = %#v", response.Apps[0])
	}
	if response.Apps[1].ID != "calendar" || response.Apps[1].IsEnabled {
		t.Fatalf("calendar app = %#v", response.Apps[1])
	}
	if directory.calls != 1 || accessible.calls != 1 {
		t.Fatalf("provider calls = %d/%d, want 1/1", directory.calls, accessible.calls)
	}
	if _, err := service.List(&AppListParams{}); err != nil {
		t.Fatalf("List(cached) error = %v", err)
	}
	if directory.calls != 1 || accessible.calls != 1 {
		t.Fatalf("cached provider calls = %d/%d, want 1/1", directory.calls, accessible.calls)
	}
	if _, err := service.List(&AppListParams{ForceRefetch: true}); err != nil {
		t.Fatalf("List(force) error = %v", err)
	}
	if directory.calls != 2 || accessible.calls != 2 {
		t.Fatalf("force provider calls = %d/%d, want 2/2", directory.calls, accessible.calls)
	}
}

func TestListAccessibleCacheIsThreadScoped(t *testing.T) {
	accessible := &threadAccessibleProvider{appsByThread: map[string][]AppEntry{
		"thread-a": {{ID: "drive-a", Name: "Drive A", IsAccessible: true}},
		"thread-b": {{ID: "drive-b", Name: "Drive B", IsAccessible: true}},
	}}
	service := NewAppService(nil)
	service.SetAccessibleProvider(accessible)
	threadA := "thread-a"
	first, err := service.List(&AppListParams{ThreadID: &threadA})
	if err != nil {
		t.Fatalf("List(thread-a) error = %v", err)
	}
	threadB := "thread-b"
	second, err := service.List(&AppListParams{ThreadID: &threadB})
	if err != nil {
		t.Fatalf("List(thread-b) error = %v", err)
	}
	if len(first.Apps) != 1 || first.Apps[0].ID != "drive-a" {
		t.Fatalf("thread-a apps = %#v", first.Apps)
	}
	if len(second.Apps) != 1 || second.Apps[0].ID != "drive-b" {
		t.Fatalf("thread-b apps = %#v", second.Apps)
	}
	if accessible.calls != 2 {
		t.Fatalf("accessible provider calls = %d, want one per thread", accessible.calls)
	}
	again, err := service.List(&AppListParams{ThreadID: &threadA})
	if err != nil {
		t.Fatalf("List(thread-a cached) error = %v", err)
	}
	if len(again.Apps) != 1 || again.Apps[0].ID != "drive-a" || accessible.calls != 2 {
		t.Fatalf("thread-a cached apps=%#v calls=%d", again.Apps, accessible.calls)
	}
}

type fakeDirectoryProvider struct {
	apps  []AppEntry
	calls int
}

func (p *fakeDirectoryProvider) ListDirectoryApps(params *AppDirectoryListParams) (*AppDirectoryListResponse, error) {
	p.calls++
	allLoaded := true
	return &AppDirectoryListResponse{Apps: cloneApps(p.apps), AllConnectorsLoaded: &allLoaded}, nil
}

type fakeAccessibleProvider struct {
	apps  []AppEntry
	calls int
}

func (p *fakeAccessibleProvider) ListAccessibleApps(params *AppAccessibleListParams) (*AppAccessibleListResponse, error) {
	p.calls++
	return &AppAccessibleListResponse{Apps: cloneApps(p.apps), CodexAppsReady: true}, nil
}

type threadAccessibleProvider struct {
	appsByThread map[string][]AppEntry
	calls        int
}

func (p *threadAccessibleProvider) ListAccessibleApps(params *AppAccessibleListParams) (*AppAccessibleListResponse, error) {
	p.calls++
	threadID := ""
	if params != nil {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	return &AppAccessibleListResponse{Apps: cloneApps(p.appsByThread[threadID]), CodexAppsReady: true}, nil
}
