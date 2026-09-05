package apps

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type metadataProviderFunc func(*AppMetadataReadParams) (*AppMetadataReadResponse, error)

func (f metadataProviderFunc) ReadAppMetadata(params *AppMetadataReadParams) (*AppMetadataReadResponse, error) {
	return f(params)
}

func TestAppToolApprovalRestrictionsNeverWeakenEitherPolicy(t *testing.T) {
	modes := []AppToolApproval{
		AppToolApprovalApprove,
		AppToolApprovalAuto,
		AppToolApprovalWrites,
		AppToolApprovalPrompt,
	}
	expected := [][]AppToolApproval{
		{AppToolApprovalApprove, AppToolApprovalAuto, AppToolApprovalWrites, AppToolApprovalPrompt},
		{AppToolApprovalAuto, AppToolApprovalAuto, AppToolApprovalPrompt, AppToolApprovalPrompt},
		{AppToolApprovalWrites, AppToolApprovalPrompt, AppToolApprovalWrites, AppToolApprovalPrompt},
		{AppToolApprovalPrompt, AppToolApprovalPrompt, AppToolApprovalPrompt, AppToolApprovalPrompt},
	}
	for parentIndex, parent := range modes {
		for requestedIndex, requested := range modes {
			if got := parent.RestrictTo(requested); got != expected[parentIndex][requestedIndex] {
				t.Fatalf("parent: %q, requested: %q -> RestrictTo = %q, want %q", parent, requested, got, expected[parentIndex][requestedIndex])
			}
		}
	}
}

func TestReadDeduplicatesOrdersMissesAndCaches(t *testing.T) {
	service := NewAppService(nil)
	calls := 0
	service.SetMetadataProvider(metadataProviderFunc(func(params *AppMetadataReadParams) (*AppMetadataReadResponse, error) {
		calls++
		if strings.Join(params.AppIDs, ",") != "beta,missing,alpha" || !params.IncludeTools {
			t.Fatalf("params = %#v", params)
		}
		return &AppMetadataReadResponse{Apps: []ConnectorMetadata{
			{ID: "alpha", Name: "Alpha", ToolSummaries: []AppToolSummary{{Name: "alpha_tool", Description: "Use Alpha"}}},
			{ID: "beta", Name: "Beta", ToolSummaries: []AppToolSummary{{Name: "beta_tool", Description: "Use Beta"}}},
		}}, nil
	}))
	response, err := service.Read(&AppsReadParams{AppIDs: []string{"beta", "missing", "alpha", "beta"}, IncludeTools: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(response.Apps) != 2 || response.Apps[0].ID != "beta" || response.Apps[1].ID != "alpha" || len(response.MissingAppIDs) != 1 || response.MissingAppIDs[0] != "missing" {
		t.Fatalf("response = %#v", response)
	}
	if _, err := service.Read(&AppsReadParams{AppIDs: []string{"alpha", "beta"}, IncludeTools: true}); err != nil {
		t.Fatalf("cached Read() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestReadRefetchesWhenToolsAreRequested(t *testing.T) {
	service := NewAppService(nil)
	calls := []bool{}
	service.SetMetadataProvider(metadataProviderFunc(func(params *AppMetadataReadParams) (*AppMetadataReadResponse, error) {
		calls = append(calls, params.IncludeTools)
		return &AppMetadataReadResponse{Apps: []ConnectorMetadata{{ID: "app", Name: "App", ToolSummaries: []AppToolSummary{{Name: "tool", Description: "Use"}}}}}, nil
	}))
	without, err := service.Read(&AppsReadParams{AppIDs: []string{"app"}})
	if err != nil || len(without.Apps) != 1 || without.Apps[0].ToolsRequested {
		t.Fatalf("without tools = %#v err=%v", without, err)
	}
	with, err := service.Read(&AppsReadParams{AppIDs: []string{"app"}, IncludeTools: true})
	if err != nil || len(with.Apps[0].ToolSummaries) != 1 || !with.Apps[0].ToolsRequested {
		t.Fatalf("with tools = %#v err=%v", with, err)
	}
	if len(calls) != 2 || calls[0] || !calls[1] {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestReadRejectsMoreThanOneHundredIDs(t *testing.T) {
	ids := make([]string, 101)
	if _, err := NewAppService(nil).Read(&AppsReadParams{AppIDs: ids}); !errors.Is(err, ErrInvalidAppRequest) {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestReadUsesStaticMetadataWithoutProvider(t *testing.T) {
	description := "Static description"
	service := NewAppService([]AppEntry{{ID: "static", Name: "Static", Description: &description}})
	response, err := service.Read(&AppsReadParams{AppIDs: []string{"static", "missing"}})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(response.Apps) != 1 || response.Apps[0].ID != "static" || response.Apps[0].Description == nil || *response.Apps[0].Description != description {
		t.Fatalf("response = %#v", response)
	}
	if len(response.MissingAppIDs) != 1 || response.MissingAppIDs[0] != "missing" {
		t.Fatalf("missing ids = %#v", response.MissingAppIDs)
	}
}

func TestAppsReadResponseJSONUsesRequiredArraysAndNullableTools(t *testing.T) {
	data, err := json.Marshal(&AppsReadResponse{Apps: []ConnectorMetadata{{ID: "app", Name: "App"}}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{`"apps":[`, `"missingAppIds":[]`, `"toolSummaries":null`} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON %s missing %s", text, want)
		}
	}
}

func TestAppToolSummaryLegacyJSONDefaultsEnabledLikeRust(t *testing.T) {
	var summary AppToolSummary
	if err := json.Unmarshal([]byte(`{"name":"search","title":"Search","description":"Search Alpha"}`), &summary); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !summary.IsEnabled || summary.DisabledReason != nil || summary.IsReadOnly {
		t.Fatalf("legacy summary = %#v", summary)
	}
	data, err := json.Marshal(&summary)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"name":"search","title":"Search","description":"Search Alpha","isEnabled":true,"disabledReason":null,"isReadOnly":false}`
	if string(data) != want {
		t.Fatalf("Marshal() = %s, want %s", data, want)
	}
}

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
	if metadata["seoDescription"] != "Find Drive files" || metadata["versionId"] != "v1" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, ok := metadata["firstPartyType"]; ok {
		t.Fatalf("metadata leaked removed firstPartyType: %#v", metadata)
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

func TestMergeConnectorsKeepsDirectoryAppsWhenAccessibleListIsEmptyLikeRust(t *testing.T) {
	merged := MergeConnectors([]AppEntry{{
		ID:           "beta",
		Name:         "Beta",
		IsAccessible: false,
	}}, nil)
	if len(merged) != 1 || merged[0].ID != "beta" || merged[0].IsAccessible {
		t.Fatalf("merged apps = %#v, want inaccessible beta directory entry", merged)
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

func TestListKeepsDirectoryWhileAccessibleProviderIsNotReadyLikeRust(t *testing.T) {
	directory := &fakeDirectoryProvider{apps: []AppEntry{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta Directory"}}}
	accessible := &readyAccessibleProvider{
		apps: []AppEntry{{
			ID:           "beta",
			Name:         "Beta",
			IsAccessible: true,
		}},
		ready: false,
	}
	service := NewAppService(nil)
	service.SetDirectoryProvider(directory)
	service.SetAccessibleProvider(accessible)
	service.SetPluginConnectors([]PluginConnector{{ID: "gamma", Name: "Gamma Plugin"}})

	response, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Apps) != 3 || response.Apps[0].ID != "beta" || !response.Apps[0].IsAccessible || response.Apps[1].ID != "alpha" || response.Apps[2].ID != "gamma" {
		t.Fatalf("unready apps = %#v, want merged beta, alpha, gamma", response.Apps)
	}

	accessible.ready = true
	if _, err := service.List(&AppListParams{ForceRefetch: true}); err != nil {
		t.Fatalf("List(force ready) error = %v", err)
	}
	ready, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List(cached ready) error = %v", err)
	}
	if len(ready.Apps) != 3 || ready.Apps[0].ID != "beta" || ready.Apps[1].ID != "alpha" || ready.Apps[2].ID != "gamma" {
		t.Fatalf("ready apps = %#v, want merged beta, alpha, gamma", ready.Apps)
	}
}

func TestCachedListForNotificationKeepsDirectoryWhenAccessibleNotReadyLikeRust(t *testing.T) {
	directory := &fakeDirectoryProvider{apps: []AppEntry{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta Directory"}}}
	accessible := &readyAccessibleProvider{
		apps: []AppEntry{{
			ID:           "beta",
			Name:         "Beta",
			IsAccessible: true,
		}},
		ready: false,
	}
	service := NewAppService(nil)
	service.SetDirectoryProvider(directory)
	service.SetAccessibleProvider(accessible)
	service.SetPluginConnectors([]PluginConnector{{ID: "gamma", Name: "Gamma Plugin"}})
	if _, err := service.List(&AppListParams{}); err != nil {
		t.Fatalf("List() error = %v", err)
	}

	cached := service.CachedListForNotification()
	if len(cached) != 3 || cached[0].ID != "beta" || !cached[0].IsAccessible || cached[1].ID != "alpha" || cached[2].ID != "gamma" {
		t.Fatalf("cached notification list = %#v, want merged beta, alpha, gamma", cached)
	}
}

func TestForceRefetchPreservesPreviousCacheOnDirectoryFailureLikeRust(t *testing.T) {
	directory := &fakeDirectoryProvider{apps: []AppEntry{{ID: "beta", Name: "Beta"}}}
	accessible := &fakeAccessibleProvider{apps: []AppEntry{{
		ID:           "beta",
		Name:         "Beta App",
		IsAccessible: true,
	}}}
	service := NewAppService(nil)
	service.SetDirectoryProvider(directory)
	service.SetAccessibleProvider(accessible)

	initial, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(initial.Apps) != 1 || initial.Apps[0].ID != "beta" || !initial.Apps[0].IsAccessible {
		t.Fatalf("initial apps = %#v", initial.Apps)
	}

	directory.err = errors.New("directory unavailable")
	if _, err := service.List(&AppListParams{ForceRefetch: true}); !errors.Is(err, directory.err) {
		t.Fatalf("List(force failure) error = %v, want %v", err, directory.err)
	}
	cached, err := service.List(&AppListParams{})
	if err != nil {
		t.Fatalf("List(cached after failure) error = %v", err)
	}
	if len(cached.Apps) != 1 || cached.Apps[0].ID != "beta" || !cached.Apps[0].IsAccessible {
		t.Fatalf("cached apps = %#v, want preserved beta", cached.Apps)
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
	err   error
	calls int
}

func (p *fakeDirectoryProvider) ListDirectoryApps(params *AppDirectoryListParams) (*AppDirectoryListResponse, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
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

type readyAccessibleProvider struct {
	apps  []AppEntry
	ready bool
	calls int
}

func (p *readyAccessibleProvider) ListAccessibleApps(params *AppAccessibleListParams) (*AppAccessibleListResponse, error) {
	p.calls++
	return &AppAccessibleListResponse{Apps: cloneApps(p.apps), CodexAppsReady: p.ready}, nil
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

func TestAppToolConfigAnalyticsResultSourceRoundTrips(t *testing.T) {
	config := AppToolConfig{AnalyticsResultSource: &AppToolResultSource{Format: "detailed_message_search_v1", SourceType: "message_id"}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal AppToolConfig: %v", err)
	}
	if !strings.Contains(string(data), `"analytics_result_source":{"format":"detailed_message_search_v1","type":"message_id"}`) {
		t.Fatalf("AppToolConfig JSON = %s", data)
	}
	parsed := AppToolConfigFromMap(map[string]any{
		"approval_mode": "prompt",
		"analytics_result_source": map[string]any{
			"format": "detailed_message_search_v1",
			"type":   "message_id",
		},
	})
	if parsed.AnalyticsResultSource == nil || parsed.AnalyticsResultSource.SourceType != "message_id" {
		t.Fatalf("AppToolConfigFromMap = %#v", parsed)
	}
}
