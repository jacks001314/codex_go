package plugin

import (
	"reflect"
	"strings"
	"testing"
)

func TestListDiscoverablePluginsFiltersAndSorts(t *testing.T) {
	available := []DiscoverableInfo{
		{ID: "b", RemotePluginID: "remote-b", Name: "Beta", AppConnectorIDs: []string{"app-b"}},
		{ID: "a", RemotePluginID: "remote-a", Name: "Alpha"},
		{ID: "c", RemotePluginID: "remote-c", Name: "Gamma"},
	}
	got := ListDiscoverablePlugins(available, &DiscoverableConfig{
		ConfiguredPluginIDs:   []string{"a", "b", "c"},
		DisabledPluginIDs:     []string{"c"},
		LoadedAppConnectorIDs: []string{"app-b"},
	})
	if ids(got) == nil {
		t.Fatalf("ids() unexpectedly nil")
	}
	if !reflect.DeepEqual(ids(got), []string{"a"}) {
		t.Fatalf("ListDiscoverablePlugins() = %v, want [a]", ids(got))
	}
}

func TestListDiscoverablePluginsTrimsFilterIDs(t *testing.T) {
	available := []DiscoverableInfo{
		{ID: "a", RemotePluginID: "remote-a", Name: "Alpha"},
		{ID: "b", RemotePluginID: "remote-b", Name: "Beta"},
		{ID: "c", RemotePluginID: "remote-c", Name: "Gamma", AppConnectorIDs: []string{" app-c "}},
	}
	got := ListDiscoverablePlugins(available, &DiscoverableConfig{
		ConfiguredPluginIDs:   []string{" remote-a ", " b ", " c "},
		DisabledPluginIDs:     []string{" b "},
		LoadedAppConnectorIDs: []string{" app-c "},
	})
	if !reflect.DeepEqual(ids(got), []string{"a"}) {
		t.Fatalf("ListDiscoverablePlugins() = %v, want [a]", ids(got))
	}
}

func TestCollectToolMentionsFromMessages(t *testing.T) {
	mentions := CollectToolMentionsFromMessages([]string{
		"run $review and [$calendar](app://calendar) but ignore @plugin://docs and $PATH",
	})
	if !mentions.PlainNames["review"] {
		t.Fatalf("plain mention review missing")
	}
	if !mentions.Paths["app://calendar"] {
		t.Fatalf("linked path mention app://calendar missing")
	}
	if mentions.Paths["plugin://docs"] {
		t.Fatalf("raw plugin mention collected as path")
	}
	if mentions.PlainNames["PATH"] {
		t.Fatalf("common env var PATH should be ignored like Rust")
	}
}

func TestCollectExplicitAppIDs(t *testing.T) {
	got := CollectExplicitAppIDs([]UserInput{
		{Type: "text", Text: "open [$calendar](app://calendar) and ignore raw $app://ignored"},
		{Type: "mention", Path: "app://mail"},
		{Type: "text", Text: "then [$team]( app://team%20drive )"},
		{Type: "mention", Path: " app://spaced "},
		{Type: "mention", Path: "plugin://docs"},
	})
	if !reflect.DeepEqual(got, map[string]bool{"calendar": true, "mail": true, "team%20drive": true}) {
		t.Fatalf("CollectExplicitAppIDs() = %v", got)
	}
}

func TestCollectExplicitPluginMentions(t *testing.T) {
	plugins := []CapabilitySummary{
		{ConfigName: "docs", DisplayName: "Docs"},
		{ConfigName: "issues", DisplayName: "Issues"},
		{ConfigName: "sample%40debug", DisplayName: "Sample"},
		{ConfigName: "raw", DisplayName: "Raw"},
	}
	got := CollectExplicitPluginMentions([]UserInput{
		{Type: "text", Text: "use [@docs](plugin://docs) and ignore raw @plugin://raw"},
		{Type: "mention", Path: "plugin://issues"},
		{Type: "text", Text: "and [@sample](plugin://sample%40debug)"},
	}, plugins)
	if names(got) == nil {
		t.Fatalf("names() unexpectedly nil")
	}
	if !reflect.DeepEqual(names(got), []string{"Docs", "Issues", "Sample"}) {
		t.Fatalf("CollectExplicitPluginMentions() = %v, want Docs Issues Sample", names(got))
	}
}

func TestCollectExplicitPluginMentionsIgnoresAppMentionsLikeRust(t *testing.T) {
	plugins := []CapabilitySummary{
		{ConfigName: "docs", DisplayName: "Docs", AppConnectors: []string{"docs-app"}},
		{ConfigName: "issues", DisplayName: "Issues", AppConnectors: []string{"issues-app"}},
	}
	got := CollectExplicitPluginMentions([]UserInput{
		{Type: "mention", Path: "app://docs-app"},
	}, plugins)
	if len(got) != 0 {
		t.Fatalf("CollectExplicitPluginMentions(app) = %v, want empty", names(got))
	}
}

func TestCollectExplicitPluginMentionsMatchesOnlyConfigNameLikeRust(t *testing.T) {
	plugins := []CapabilitySummary{
		{ConfigName: "docs", RemotePluginID: "remote-docs", Name: "docs-name", DisplayName: "Docs"},
	}
	if got := CollectExplicitPluginMentions([]UserInput{{Type: "mention", Path: "plugin://Docs"}}, plugins); len(got) != 0 {
		t.Fatalf("display-name path matched plugin unexpectedly: %v", names(got))
	}
	if got := CollectExplicitPluginMentions([]UserInput{{Type: "mention", Path: " plugin://docs "}}, plugins); len(got) != 0 {
		t.Fatalf("spaced structured path matched plugin unexpectedly: %v", names(got))
	}
}

func TestRenderExplicitPluginInstructions(t *testing.T) {
	text, ok := RenderExplicitPluginInstructions(&CapabilitySummary{
		DisplayName: "Docs",
		HasSkills:   true,
	}, []string{"docs-mcp"}, []string{"Docs App"})
	if !ok {
		t.Fatalf("RenderExplicitPluginInstructions() ok = false, want true")
	}
	for _, want := range []string{
		"Capabilities from the `Docs` plugin:",
		"prefixed with `Docs:`",
		"tool_search",
		"Apps from this plugin available in this session: `Docs App`.",
		"`docs-mcp`",
		"Use these plugin-associated capabilities",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("RenderExplicitPluginInstructions() missing %q in:\n%s", want, text)
		}
	}
}

func TestRenderExplicitPluginInstructionsReturnsFalseForNoCapabilities(t *testing.T) {
	if text, ok := RenderExplicitPluginInstructions(&CapabilitySummary{DisplayName: "Docs"}, nil, nil); ok || text != "" {
		t.Fatalf("RenderExplicitPluginInstructions(no capabilities) = %q/%v, want empty/false", text, ok)
	}
}

func TestBuildPluginInjections(t *testing.T) {
	got := BuildPluginInjections(
		[]CapabilitySummary{{DisplayName: "Docs", HasSkills: true}},
		[]ToolInfo{
			{ServerName: "docs-mcp", PluginDisplayNames: []string{"Docs"}},
			{ServerName: AppsMCPServerName, PluginDisplayNames: []string{"Docs"}},
			{ServerName: "other", PluginDisplayNames: []string{"Other"}},
		},
		[]AppInfo{
			{ID: "docs", DisplayName: "Docs App", Enabled: true, PluginDisplayNames: []string{"Docs"}},
			{ID: "disabled", DisplayName: "Disabled", Enabled: false, PluginDisplayNames: []string{"Docs"}},
		},
	)
	if len(got) != 1 {
		t.Fatalf("BuildPluginInjections() len = %d, want 1", len(got))
	}
	if !strings.Contains(got[0], "`docs-mcp`") || !strings.Contains(got[0], "`Docs App`") {
		t.Fatalf("BuildPluginInjections() = %q", got[0])
	}
	if strings.Contains(got[0], AppsMCPServerName) || strings.Contains(got[0], "Disabled") {
		t.Fatalf("BuildPluginInjections() included filtered capability: %q", got[0])
	}
}

func ids(plugins []DiscoverableInfo) []string {
	out := make([]string, len(plugins))
	for i := range plugins {
		out[i] = plugins[i].ID
	}
	return out
}

func names(plugins []CapabilitySummary) []string {
	out := make([]string, len(plugins))
	for i := range plugins {
		out[i] = plugins[i].DisplayName
	}
	return out
}

func TestMentionIDFromPathIgnoresQueryParameters(t *testing.T) {
	if got := pluginConfigNameFromPath("plugin://acme/weather?app=calendar&browserFamily=chrome"); got != "acme/weather" {
		t.Fatalf("pluginConfigNameFromPath = %q", got)
	}
}

func TestCollectExplicitPluginIDsIgnoresDisplayNamesAndQueryParameters(t *testing.T) {
	got := CollectExplicitPluginIDs([]UserInput{
		{Type: "text", Text: "use [@alias](plugin://sample@test?app=calendar&browserFamily=chrome)"},
		{Type: "mention", Path: "plugin://selected-two"},
	})
	want := map[string]bool{
		"sample@test":  true,
		"selected-two": true,
	}
	if len(got) != len(want) {
		t.Fatalf("CollectExplicitPluginIDs() = %#v, want %#v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("CollectExplicitPluginIDs() = %#v, want %#v", got, want)
		}
	}
}
